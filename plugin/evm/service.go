// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package evm

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/ava-labs/avalanchego/api"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto"
	"github.com/ava-labs/avalanchego/utils/formatting"
	"github.com/ava-labs/avalanchego/utils/json"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
)

const (
	version = "coreth"
)

// test constants
const (
	GenesisTestAddr = "0x751a0b96e1042bee789452ecb20253fba40dbe85"
	GenesisTestKey  = "0xabd71b35d559563fea757f0f5edbde286fb8c043105b15abb7cd57189306d7d1"

	// Max number of addresses that can be passed in as argument to GetUTXOs
	maxGetUTXOsAddrs = 1024
)

var (
	errNoAddresses   = errors.New("no addresses provided")
	errNoSourceChain = errors.New("no source chain provided")
)

// SnowmanAPI introduces snowman specific functionality to the evm
type SnowmanAPI struct{ vm *VM }

// AvaxAPI offers Avalanche network related API methods
type AvaxAPI struct{ vm *VM }

// NetAPI offers network related API methods
type NetAPI struct{ vm *VM }

// Listening returns an indication if the node is listening for network connections.
func (s *NetAPI) Listening() bool { return true } // always listening

// PeerCount returns the number of connected peers
func (s *NetAPI) PeerCount() hexutil.Uint { return hexutil.Uint(0) } // TODO: report number of connected peers

// Version returns the current ethereum protocol version.
func (s *NetAPI) Version() string { return fmt.Sprintf("%d", s.vm.networkID) }

// Web3API offers helper API methods
type Web3API struct{}

// ClientVersion returns the version of the vm running
func (s *Web3API) ClientVersion() string { return version }

// Sha3 returns the bytes returned by hashing [input] with Keccak256
func (s *Web3API) Sha3(input hexutil.Bytes) hexutil.Bytes { return ethcrypto.Keccak256(input) }

// GetAcceptedFrontReply defines the reply that will be sent from the
// GetAcceptedFront API call
type GetAcceptedFrontReply struct {
	Hash   common.Hash `json:"hash"`
	Number *big.Int    `json:"number"`
}

// GetAcceptedFront returns the last accepted block's hash and height
func (api *SnowmanAPI) GetAcceptedFront(ctx context.Context) (*GetAcceptedFrontReply, error) {
	blk := api.vm.getLastAccepted().ethBlock
	return &GetAcceptedFrontReply{
		Hash:   blk.Hash(),
		Number: blk.Number(),
	}, nil
}

// IssueBlock to the chain
func (api *SnowmanAPI) IssueBlock(ctx context.Context) error {
	log.Info("Issuing a new block")

	return api.vm.tryBlockGen()
}

// ClientVersion returns the version of the vm running
func (service *AvaxAPI) ClientVersion() string { return version }

// ExportKeyArgs are arguments for ExportKey
type ExportKeyArgs struct {
	api.UserPass
	Address string `json:"address"`
}

// ExportKeyReply is the response for ExportKey
type ExportKeyReply struct {
	// The decrypted PrivateKey for the Address provided in the arguments
	PrivateKey    string `json:"privateKey"`
	PrivateKeyHex string `json:"privateKeyHex"`
}

// ExportKey returns a private key from the provided user
func (service *AvaxAPI) ExportKey(r *http.Request, args *ExportKeyArgs, reply *ExportKeyReply) error {
	log.Info("EVM: ExportKey called")

	address, err := ParseEthAddress(args.Address)
	if err != nil {
		return fmt.Errorf("couldn't parse %s to address: %s", args.Address, err)
	}

	db, err := service.vm.ctx.Keystore.GetDatabase(args.Username, args.Password)
	if err != nil {
		return fmt.Errorf("problem retrieving user '%s': %w", args.Username, err)
	}
	defer db.Close()

	user := user{db: db}
	sk, err := user.getKey(address)
	if err != nil {
		return fmt.Errorf("problem retrieving private key: %w", err)
	}
	reply.PrivateKey = constants.SecretKeyPrefix + formatting.CB58{Bytes: sk.Bytes()}.String()
	reply.PrivateKeyHex = hexutil.Encode(sk.Bytes())
	return nil
}

// ImportKeyArgs are arguments for ImportKey
type ImportKeyArgs struct {
	api.UserPass
	PrivateKey string `json:"privateKey"`
}

// ImportKey adds a private key to the provided user
func (service *AvaxAPI) ImportKey(r *http.Request, args *ImportKeyArgs, reply *api.JSONAddress) error {
	log.Info(fmt.Sprintf("EVM: ImportKey called for user '%s'", args.Username))

	if !strings.HasPrefix(args.PrivateKey, constants.SecretKeyPrefix) {
		return fmt.Errorf("private key missing %s prefix", constants.SecretKeyPrefix)
	}

	trimmedPrivateKey := strings.TrimPrefix(args.PrivateKey, constants.SecretKeyPrefix)
	formattedPrivateKey := formatting.CB58{}
	if err := formattedPrivateKey.FromString(trimmedPrivateKey); err != nil {
		return fmt.Errorf("problem parsing private key: %w", err)
	}

	factory := crypto.FactorySECP256K1R{}
	skIntf, err := factory.ToPrivateKey(formattedPrivateKey.Bytes)
	if err != nil {
		return fmt.Errorf("problem parsing private key: %w", err)
	}
	sk := skIntf.(*crypto.PrivateKeySECP256K1R)

	// TODO: return eth address here
	reply.Address = FormatEthAddress(GetEthAddress(sk))

	db, err := service.vm.ctx.Keystore.GetDatabase(args.Username, args.Password)
	if err != nil {
		return fmt.Errorf("problem retrieving data: %w", err)
	}
	defer db.Close()

	user := user{db: db}
	if err := user.putAddress(sk); err != nil {
		return fmt.Errorf("problem saving key %w", err)
	}
	return nil
}

// ImportArgs are arguments for passing into Import requests
type ImportArgs struct {
	api.UserPass

	// Chain the funds are coming from
	SourceChain string `json:"sourceChain"`

	// The address that will receive the imported funds
	To string `json:"to"`
}

// ImportAVAX is a deprecated name for Import.
func (service *AvaxAPI) ImportAVAX(_ *http.Request, args *ImportArgs, response *api.JSONTxID) error {
	return service.Import(nil, args, response)
}

// Import issues a transaction to import AVAX from the X-chain. The AVAX
// must have already been exported from the X-Chain.
func (service *AvaxAPI) Import(_ *http.Request, args *ImportArgs, response *api.JSONTxID) error {
	log.Info("EVM: ImportAVAX called")

	chainID, err := service.vm.ctx.BCLookup.Lookup(args.SourceChain)
	if err != nil {
		return fmt.Errorf("problem parsing chainID %q: %w", args.SourceChain, err)
	}

	to, err := ParseEthAddress(args.To)
	if err != nil { // Parse address
		return fmt.Errorf("couldn't parse argument 'to' to an address: %w", err)
	}

	// Get the user's info
	db, err := service.vm.ctx.Keystore.GetDatabase(args.Username, args.Password)
	if err != nil {
		return fmt.Errorf("couldn't get user '%s': %w", args.Username, err)
	}
	defer db.Close()

	user := user{db: db}
	privKeys, err := user.getKeys()
	if err != nil { // Get keys
		return fmt.Errorf("couldn't get keys controlled by the user: %w", err)
	}

	tx, err := service.vm.newImportTx(chainID, to, privKeys)
	if err != nil {
		return err
	}

	response.TxID = tx.ID()
	return service.vm.issueTx(tx)
}

// ExportAVAXArgs are the arguments to ExportAVAX
type ExportAVAXArgs struct {
	api.UserPass

	// AssetID of the tokens
	AssetID ids.ID `json:"assetID"`
	// Amount of asset to send
	Amount json.Uint64 `json:"amount"`

	// ID of the address that will receive the AVAX. This address includes the
	// chainID, which is used to determine what the destination chain is.
	To string `json:"to"`
}

// ExportAVAX exports AVAX from the C-Chain to the X-Chain
// It must be imported on the X-Chain to complete the transfer
func (service *AvaxAPI) ExportAVAX(_ *http.Request, args *ExportAVAXArgs, response *api.JSONTxID) error {
	return service.Export(nil, &ExportArgs{
		ExportAVAXArgs: *args,
		AssetID:        service.vm.ctx.AVAXAssetID,
	}, response)
}

// ExportArgs are the arguments to Export
type ExportArgs struct {
	ExportAVAXArgs
	// AssetID of the tokens
	AssetID ids.ID `json:"assetID"`
}

// Export exports an asset from the C-Chain to the X-Chain
// It must be imported on the X-Chain to complete the transfer
func (service *AvaxAPI) Export(_ *http.Request, args *ExportArgs, response *api.JSONTxID) error {
	log.Info("EVM: Export called")
	if args.AssetID.IsZero() {
		return fmt.Errorf("assetID is required")
	}

	if args.Amount == 0 {
		return errors.New("argument 'amount' must be > 0")
	}

	chainID, to, err := service.vm.ParseAddress(args.To)
	if err != nil {
		return err
	}

	// Get this user's data
	db, err := service.vm.ctx.Keystore.GetDatabase(args.Username, args.Password)
	if err != nil {
		return fmt.Errorf("problem retrieving user '%s': %w", args.Username, err)
	}
	defer db.Close()

	user := user{db: db}
	privKeys, err := user.getKeys()
	if err != nil {
		return fmt.Errorf("couldn't get addresses controlled by the user: %w", err)
	}

	// Create the transaction
	tx, err := service.vm.newExportTx(
		args.AssetID,        // AssetID
		uint64(args.Amount), // Amount
		chainID,             // ID of the chain to send the funds to
		to,                  // Address
		privKeys,            // Private keys
	)
	if err != nil {
		return fmt.Errorf("couldn't create tx: %w", err)
	}

	response.TxID = tx.ID()
	return service.vm.issueTx(tx)
}

// Index is an address and an associated UTXO.
// Marks a starting or stopping point when fetching UTXOs. Used for pagination.
type Index struct {
	Address string `json:"address"` // The address as a string
	UTXO    string `json:"utxo"`    // The UTXO ID as a string
}

// GetUTXOsArgs are arguments for passing into GetUTXOs.
// Gets the UTXOs that reference at least one address in [Addresses].
// Returns at most [limit] addresses.
// If specified, [SourceChain] is the chain where the atomic UTXOs were exported from. If not specified,
// then GetUTXOs returns an error since the C Chain only has atomic UTXOs.
// If [limit] == 0 or > [maxUTXOsToFetch], fetches up to [maxUTXOsToFetch].
// [StartIndex] defines where to start fetching UTXOs (for pagination.)
// UTXOs fetched are from addresses equal to or greater than [StartIndex.Address]
// For address [StartIndex.Address], only UTXOs with IDs greater than [StartIndex.UTXO] will be returned.
// If [StartIndex] is omitted, gets all UTXOs.
// If GetUTXOs is called multiple times, with our without [StartIndex], it is not guaranteed
// that returned UTXOs are unique. That is, the same UTXO may appear in the response of multiple calls.
type GetUTXOsArgs struct {
	Addresses   []string    `json:"addresses"`
	SourceChain string      `json:"sourceChain"`
	Limit       json.Uint32 `json:"limit"`
	StartIndex  Index       `json:"startIndex"`
	Encoding    string      `json:"encoding"`
}

// GetUTXOsReply defines the GetUTXOs replies returned from the API
type GetUTXOsReply struct {
	// Number of UTXOs returned
	NumFetched json.Uint64 `json:"numFetched"`
	// The UTXOs
	UTXOs []string `json:"utxos"`
	// The last UTXO that was returned, and the address it corresponds to.
	// Used for pagination. To get the rest of the UTXOs, call GetUTXOs
	// again and set [StartIndex] to this value.
	EndIndex Index `json:"endIndex"`
	// Encoding specifies the encoding format the UTXOs are returned in
	Encoding string `json:"encoding"`
}

// GetUTXOs gets all utxos for passed in addresses
func (service *AvaxAPI) GetUTXOs(r *http.Request, args *GetUTXOsArgs, reply *GetUTXOsReply) error {
	service.vm.ctx.Log.Info("EVM: GetUTXOs called for with %s", args.Addresses)

	if len(args.Addresses) == 0 {
		return errNoAddresses
	}
	if len(args.Addresses) > maxGetUTXOsAddrs {
		return fmt.Errorf("number of addresses given, %d, exceeds maximum, %d", len(args.Addresses), maxGetUTXOsAddrs)
	}

	encoding, err := service.vm.encodingManager.GetEncoding(args.Encoding)
	if err != nil {
		return fmt.Errorf("problem getting encoding formatter for '%s': %w", args.Encoding, err)
	}

	sourceChain := ids.ID{}
	if args.SourceChain == "" {
		return errNoSourceChain
	}

	chainID, err := service.vm.ctx.BCLookup.Lookup(args.SourceChain)
	if err != nil {
		return fmt.Errorf("problem parsing source chainID %q: %w", args.SourceChain, err)
	}
	sourceChain = chainID

	addrSet := ids.ShortSet{}
	for _, addrStr := range args.Addresses {
		addr, err := service.vm.ParseLocalAddress(addrStr)
		if err != nil {
			return fmt.Errorf("couldn't parse address %q: %w", addrStr, err)
		}
		addrSet.Add(addr)
	}

	startAddr := ids.ShortEmpty
	startUTXO := ids.Empty
	if args.StartIndex.Address != "" || args.StartIndex.UTXO != "" {
		startAddr, err = service.vm.ParseLocalAddress(args.StartIndex.Address)
		if err != nil {
			return fmt.Errorf("couldn't parse start index address %q: %w", args.StartIndex.Address, err)
		}
		startUTXO, err = ids.FromString(args.StartIndex.UTXO)
		if err != nil {
			return fmt.Errorf("couldn't parse start index utxo: %w", err)
		}
	}

	utxos, endAddr, endUTXOID, err := service.vm.GetAtomicUTXOs(
		sourceChain,
		addrSet,
		startAddr,
		startUTXO,
		int(args.Limit),
	)
	if err != nil {
		return fmt.Errorf("problem retrieving UTXOs: %w", err)
	}

	reply.UTXOs = make([]string, len(utxos))
	for i, utxo := range utxos {
		b, err := service.vm.codec.Marshal(utxo)
		if err != nil {
			return fmt.Errorf("problem marshalling UTXO: %w", err)
		}
		reply.UTXOs[i] = encoding.ConvertBytes(b)
	}

	endAddress, err := service.vm.FormatLocalAddress(endAddr)
	if err != nil {
		return fmt.Errorf("problem formatting address: %w", err)
	}

	reply.EndIndex.Address = endAddress
	reply.EndIndex.UTXO = endUTXOID.String()
	reply.NumFetched = json.Uint64(len(utxos))
	reply.Encoding = encoding.Encoding()
	return nil
}

// IssueTx ...
func (service *AvaxAPI) IssueTx(r *http.Request, args *api.FormattedTx, response *api.JSONTxID) error {
	log.Info("EVM: IssueTx called")

	encoding, err := service.vm.encodingManager.GetEncoding(args.Encoding)
	if err != nil {
		return fmt.Errorf("problem getting encoding formatter for '%s': %w", args.Encoding, err)
	}
	txBytes, err := encoding.ConvertString(args.Tx)
	if err != nil {
		return fmt.Errorf("problem decoding transaction: %w", err)
	}

	tx := &Tx{}
	if err := service.vm.codec.Unmarshal(txBytes, tx); err != nil {
		return fmt.Errorf("problem parsing transaction: %w", err)
	}
	if err := tx.Sign(service.vm.codec, nil); err != nil {
		return fmt.Errorf("problem initializing transaction: %w", err)
	}

	utx, ok := tx.UnsignedTx.(UnsignedAtomicTx)
	if !ok {
		return errors.New("cannot issue non-atomic transaction through IssueTx API")
	}

	if err := utx.SemanticVerify(service.vm, tx); err != nil {
		return err
	}

	response.TxID = tx.ID()
	return service.vm.issueTx(tx)
}
