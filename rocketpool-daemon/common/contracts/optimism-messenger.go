package contracts

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	batch "github.com/rocket-pool/batch-query"
	"github.com/rocket-pool/node-manager-core/eth"
	"github.com/rocket-pool/rocketpool-go/core"
)

const (
	optimismMessengerAbiString string = `[{"inputs":[],"name":"rateStale","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"submitRate","outputs":[],"stateMutability":"nonpayable","type":"function"}]`
)

// ABI cache
var optimismMessengerAbi abi.ABI
var optimismOnce sync.Once

// ===============
// === Structs ===
// ===============

// Binding for the Optimism Messenger
type OptimismMessenger struct {

	// === Internal fields ===
	contract *core.Contract
}

// ====================
// === Constructors ===
// ====================

// Creates a new Optimism Messenger contract binding
func NewOptimismMessenger(address common.Address, client core.ExecutionClient) (*OptimismMessenger, error) {
	// Parse the ABI
	var err error
	optimismOnce.Do(func() {
		var parsedAbi abi.ABI
		parsedAbi, err = abi.JSON(strings.NewReader(optimismMessengerAbiString))
		if err == nil {
			optimismMessengerAbi = parsedAbi
		}
	})
	if err != nil {
		return nil, fmt.Errorf("error parsing Optimism messenger ABI: %w", err)
	}

	// Create the contract
	contract := &core.Contract{
		Contract: bind.NewBoundContract(address, optimismMessengerAbi, client, client, client),
		Address:  &address,
		ABI:      &optimismMessengerAbi,
		Client:   client,
	}

	return &OptimismMessenger{
		contract: contract,
	}, nil
}

// =============
// === Calls ===
// =============

// Check if the RPL rate is stale and needs to be updated
func (c *OptimismMessenger) IsRateStale(mc *batch.MultiCaller, out *bool) {
	core.AddCall(mc, c.contract, out, "rateStale")
}

// ====================
// === Transactions ===
// ====================

// Send the latest RPL rate to the L2
func (c *OptimismMessenger) SubmitRate(opts *bind.TransactOpts) (*eth.TransactionInfo, error) {
	return core.NewTransactionInfo(c.contract, "submitRate", opts)
}
