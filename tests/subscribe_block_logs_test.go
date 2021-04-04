package tests

import (
	"context"
	"math/big"
	"testing"

	"github.com/ava-labs/coreth/eth/filters"

	"github.com/ava-labs/coreth/core/types"
	"github.com/ethereum/go-ethereum/common"
)

func TestBlockLogsAllowUnfinalized(t *testing.T) {
	chain, newBlockChan, newTxPoolHeadChan, txSubmitCh := NewDefaultChain(t)

	// Override SetOnSealFinish set in NewDefaultChain, so that each sealed block
	// is set as the new preferred block within this test.
	chain.SetOnSealFinish(func(block *types.Block) error {
		if err := chain.SetPreference(block); err != nil {
			t.Fatal(err)
		}
		newBlockChan <- block
		return nil
	})

	chain.Start()
	defer chain.Stop()

	acceptedLogsCh := make(chan []*types.Log, 1000)
	ethBackend := chain.Backend().APIBackend
	ethBackend.SubscribeAcceptedLogsEvent(acceptedLogsCh)

	api := filters.NewPublicFilterAPI(ethBackend, true, 10)

	// *NOTE* this was pre-compiled for the test..
	/*
		pragma solidity >=0.6.0;

		contract Counter {
		    uint256 x;

		    event CounterEmit(uint256 indexed oldval, uint256 indexed newval);

		    constructor() public {
		        emit CounterEmit(0, 42);
		        x = 42;
		    }

		    function add(uint256 y) public returns (uint256) {
		        x = x + y;
		        emit CounterEmit(y, x);
		        return x;
		    }
		}
	*/
	// contracts, err := compiler.CompileSolidityString("", src)
	// checkError(err)
	// contract, _ := contracts[fmt.Sprintf("%s:%s", ".", "Counter")]
	// _ = contract

	// solc-linux-amd64-v0.6.12+commit.27d51765 --bin -o counter.bin counter.sol

	code := common.Hex2Bytes(
		"608060405234801561001057600080fd5b50602a60007f53564ba0be98bdbd40460eb78d2387edab91de6a842e1449053dae1f07439a3160405160405180910390a3602a60008190555060e9806100576000396000f3fe6080604052348015600f57600080fd5b506004361060285760003560e01c80631003e2d214602d575b600080fd5b605660048036036020811015604157600080fd5b8101908080359060200190929190505050606c565b6040518082815260200191505060405180910390f35b60008160005401600081905550600054827f53564ba0be98bdbd40460eb78d2387edab91de6a842e1449053dae1f07439a3160405160405180910390a3600054905091905056fea2646970667358221220dd9c84516cd903bf6a151cbdaef2f2514c28f2f422782a388a2774412b81f08864736f6c634300060c0033",
		// contract.Code[2:],
	)

	tx := types.NewContractCreation(uint64(0), big.NewInt(0), uint64(gasLimit), gasPrice, code)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), fundedKey.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range chain.AddRemoteTxs([]*types.Transaction{signedTx}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	<-txSubmitCh
	chain.GenBlock()

	block := <-newBlockChan
	<-newTxPoolHeadChan

	if block.NumberU64() != uint64(1) {
		t.Fatalf("Expected to create a new block with height 1, but found %d", block.NumberU64())
	}

	ctx := context.Background()
	fc := filters.FilterCriteria{
		FromBlock: big.NewInt(1),
	}

	fid, err := api.NewFilter(fc)
	if err != nil {
		t.Fatalf("Failed to create NewFilter due to %s", err)
	}

	chain.BlockChain().GetVMConfig().AllowUnfinalizedQueries = true
	logs, err := api.GetLogs(ctx, fc)
	if err != nil {
		t.Fatalf("GetLogs failed due to %s", err)
	}
	if len(logs) != 1 {
		t.Fatalf("Expected GetLogs to return 1 log, but found %d", len(logs))
	}
	if logs[0].BlockNumber != 1 {
		t.Fatalf("Expected GetLogs to return 1 log with block number 1, but found block number %d", logs[0].BlockNumber)
	}

	logs, err = api.GetFilterLogs(ctx, fid)
	if err != nil {
		t.Fatalf("GetFilter failed due to %s", err)
	}
	if len(logs) != 1 {
		t.Fatalf("Expected GetFilterLogs to return 1 log, but found %d", len(logs))
	}
	if logs[0].BlockNumber != 1 {
		t.Fatalf("Expected GetFilterLogs to return 1 log with BlockNumber 1, but found BlockNumber %d", logs[0].BlockNumber)
	}

	chain.BlockChain().GetVMConfig().AllowUnfinalizedQueries = false
	logs, err = api.GetLogs(ctx, fc)
	if logs != nil {
		t.Fatalf("Expected logs to be empty, but found %d logs", len(logs))
	}
	if err == nil || err.Error() != "requested from block 1 after last accepted block 0" {
		t.Fatalf("Expected GetLogs to error due to requesting above last accepted block, but found error %s", err)
	}

	fc2 := filters.FilterCriteria{
		FromBlock: big.NewInt(0),
		ToBlock:   big.NewInt(1),
	}
	logs, err = api.GetLogs(ctx, fc2)
	if logs != nil {
		t.Fatalf("Expected GetLogs to return empty, but found %d logs", len(logs))
	}
	if err == nil || err.Error() != "requested to block 1 after last accepted block 0" {
		t.Fatalf("Expected GetLogs to error due to requesting block above last accepted block, but found error %s", err)
	}

	logs, err = api.GetFilterLogs(ctx, fid)
	if logs != nil {
		t.Fatalf("Expected GetFilterLogs to return empty logs, but found %d logs", len(logs))
	}
	if err == nil || err.Error() != "requested from block 1 after last accepted block 0" {
		t.Fatalf("Expected GetLogs to fail due to requesting block above last accepted block, but found error %s", err)
	}

	fid2, err := api.NewFilter(fc2)
	if err != nil {
		t.Fatalf("NewFilter failed due to %s", err)
	}
	logs, err = api.GetFilterLogs(ctx, fid2)
	if logs != nil {
		t.Fatalf("Expected GetFilterLogs to return empty logs but found %d logs", len(logs))
	}
	if err == nil || err.Error() != "requested to block 1 after last accepted block 0" {
		t.Fatalf("Expected GetFilterLogs to fail due to requesting block above last accepted block, but found error %s", err)
	}

	select {
	case <-acceptedLogsCh:
		t.Fatal("Received accepted logs event before Accepting block")
	default:
	}

	if err := chain.Accept(block); err != nil {
		t.Fatal(err)
	}

	chain.BlockChain().GetVMConfig().AllowUnfinalizedQueries = false
	logs, err = api.GetLogs(ctx, fc)
	if err != nil {
		t.Fatalf("GetLogs failed due to %s", err)
	}
	if len(logs) != 1 {
		t.Fatalf("Expected GetLogs to return 1 log, but found %d", len(logs))
	}
	if logs[0].BlockNumber != 1 {
		t.Fatalf("Expected single log to have block number 1, but found %d", logs[0].BlockNumber)
	}

	logs, err = api.GetFilterLogs(ctx, fid)
	if err != nil {
		t.Fatalf("GetFilterLogs failed due to %s", err)
	}
	if len(logs) != 1 {
		t.Fatalf("Expected GetFilterLogs to return 1 log, but found %d", len(logs))
	}
	if logs[0].BlockNumber != 1 {
		t.Fatalf("Expected GetFilterLogs to return 1 log with BlocKNumber 1, but found BlockNumber %d", logs[0].BlockNumber)
	}

	select {
	case acceptedLogs := <-acceptedLogsCh:
		if len(acceptedLogs) != 1 {
			t.Fatalf("Expected accepted logs channel to return 1 log, but found %d", len(acceptedLogs))
		}
		if acceptedLogs[0].BlockNumber != 1 {
			t.Fatalf("Expected accepted logs channel to return 1 log with BlockNumber 1, but found BlockNumber %d", acceptedLogs[0].BlockNumber)
		}
	default:
		t.Fatal("Failed to receive logs via accepted logs channel")
	}
}
