// Copyright (C) 2019-2023, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package evm

import (
	"fmt"
	"sync"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ethereum/go-ethereum/log"

	"github.com/ava-labs/coreth/gossip"
	"github.com/ava-labs/coreth/plugin/evm/message"

	bloomfilter "github.com/holiman/bloomfilter/v2"

	"github.com/ava-labs/coreth/core"
	"github.com/ava-labs/coreth/core/txpool"
	"github.com/ava-labs/coreth/core/types"
)

var (
	_ gossip.Set[*GossipAtomicTx] = (*GossipAtomicMempool)(nil)
	_ gossip.Gossipable           = (*GossipAtomicTx)(nil)

	_ gossip.Set[*GossipEthTx] = (*GossipEthTxPool)(nil)
	_ gossip.Gossipable        = (*GossipEthTx)(nil)
)

func NewGossipAtomicMempool(Mempool *Mempool) (*GossipAtomicMempool, error) {
	bloom, err := bloomfilter.New(gossip.DefaultBloomM, gossip.DefaultBloomK)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize bloom filter: %w", err)
	}

	return &GossipAtomicMempool{
		mempool: Mempool,
		bloom:   bloom,
	}, nil
}

type GossipAtomicMempool struct {
	mempool *Mempool
	bloom   *bloomfilter.Filter
	lock    sync.RWMutex
}

func (g *GossipAtomicMempool) Add(tx *GossipAtomicTx) (bool, error) {
	ok, err := g.mempool.AddTx(tx.Tx)
	if err != nil {
		if !tx.Local {
			// unlike local txs, invalid remote txs are recorded as discarded
			// so that they won't be requested again
			txID := tx.Tx.ID()
			g.mempool.discardedTxs.Put(txID, tx.Tx)
			log.Debug("failed to issue remote tx to mempool",
				"txID", txID,
				"err", err,
			)
		}
		return false, err
	}

	if !ok {
		return false, nil
	}

	g.lock.Lock()
	defer g.lock.Unlock()

	g.bloom.Add(gossip.NewHasher(tx.GetID()))
	g.bloom, _ = gossip.ResetBloomFilterIfNeeded(g.bloom, gossip.DefaultBloomMaxFilledRatio)

	return true, nil
}

func (g *GossipAtomicMempool) Get(filter func(tx *GossipAtomicTx) bool) []*GossipAtomicTx {
	f := func(tx *Tx) bool {
		return filter(&GossipAtomicTx{
			Tx: tx,
		})
	}
	txs := g.mempool.GetTxs(f)
	gossipTxs := make([]*GossipAtomicTx, 0, len(txs))
	for _, tx := range txs {
		gossipTxs = append(gossipTxs, &GossipAtomicTx{
			Tx: tx,
		})
	}

	return gossipTxs
}

func (g *GossipAtomicMempool) GetBloomFilter() ([]byte, error) {
	g.lock.RLock()
	defer g.lock.RUnlock()

	return g.bloom.MarshalBinary()
}

type GossipAtomicTx struct {
	Tx    *Tx
	Local bool
}

func (tx *GossipAtomicTx) GetID() ids.ID {
	return tx.Tx.ID()
}

func (tx *GossipAtomicTx) Marshal() ([]byte, error) {
	return Codec.Marshal(message.Version, tx.Tx)
}

func (tx *GossipAtomicTx) Unmarshal(bytes []byte) error {
	tx.Tx = &Tx{}
	_, err := Codec.Unmarshal(bytes, tx.Tx)

	return err
}

func NewGossipEthTxPool(mempool *txpool.TxPool) (*GossipEthTxPool, error) {
	bloom, err := bloomfilter.New(gossip.DefaultBloomM, gossip.DefaultBloomK)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize bloom filter: %w", err)
	}

	g := &GossipEthTxPool{
		mempool:    mempool,
		pendingTxs: make(chan core.NewTxsEvent),
		bloom:      bloom,
	}
	return g, nil
}

type GossipEthTxPool struct {
	mempool    *txpool.TxPool
	pendingTxs chan core.NewTxsEvent

	bloom *bloomfilter.Filter
	lock  sync.RWMutex
}

func (g *GossipEthTxPool) Subscribe(shutdownChan chan struct{}, shutdownWg *sync.WaitGroup) {
	defer shutdownWg.Done()
	g.mempool.SubscribeNewTxsEvent(g.pendingTxs)

	for {
		select {
		case <-shutdownChan:
			log.Debug("shutting down subscription")
			return
		case tx := <-g.pendingTxs:
			g.lock.Lock()
			for _, tx := range tx.Txs {
				g.bloom.Add(gossip.NewHasher(ids.ID(tx.Hash())))
				g.bloom, _ = gossip.ResetBloomFilterIfNeeded(g.bloom, gossip.DefaultBloomMaxFilledRatio)
			}
			g.lock.Unlock()
		}
	}
}

// Add enqueues the transaction to the mempool. Subscribe should be called
// to receive an event if tx is actually added to the mempool or not.
func (g *GossipEthTxPool) Add(tx *GossipEthTx) (bool, error) {
	err := g.mempool.AddRemotes([]*types.Transaction{tx.Tx})[0]
	if err != nil {
		return false, err
	}

	return true, nil
}

func (g *GossipEthTxPool) Get(filter func(tx *GossipEthTx) bool) []*GossipEthTx {
	pending, _ := g.mempool.Content()
	result := make([]*GossipEthTx, 0)

	for _, txs := range pending {
		for _, tx := range txs {
			gossipTx := &GossipEthTx{Tx: tx}
			if !filter(gossipTx) {
				continue
			}

			result = append(result, gossipTx)
		}
	}

	return result
}

func (g *GossipEthTxPool) GetBloomFilter() ([]byte, error) {
	g.lock.RLock()
	defer g.lock.RUnlock()

	return g.bloom.MarshalBinary()
}

type GossipEthTx struct {
	Tx *types.Transaction
}

func (tx *GossipEthTx) GetID() ids.ID {
	return ids.ID(tx.Tx.Hash())
}

func (tx *GossipEthTx) Marshal() ([]byte, error) {
	return tx.Tx.MarshalBinary()
}

func (tx *GossipEthTx) Unmarshal(bytes []byte) error {
	tx.Tx = &types.Transaction{}
	err := tx.Tx.UnmarshalBinary(bytes)

	return err
}
