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
	polygonMessengerAbiString string = `[{"inputs":[],"name":"rateStale","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"submitRate","outputs":[],"stateMutability":"nonpayable","type":"function"}]`
)

// ABI cache
var polygonMessengerAbi abi.ABI
var polygonOnce sync.Once

// ===============
// === Structs ===
// ===============

// Binding for the Polygon Messenger
type PolygonMessenger struct {

	// === Internal fields ===
	contract *core.Contract
}

// ====================
// === Constructors ===
// ====================

// Creates a new Polygon Messenger contract binding
func NewPolygonMessenger(address common.Address, client core.ExecutionClient) (*PolygonMessenger, error) {
	// Parse the ABI
	var err error
	polygonOnce.Do(func() {
		var parsedAbi abi.ABI
		parsedAbi, err = abi.JSON(strings.NewReader(polygonMessengerAbiString))
		if err == nil {
			polygonMessengerAbi = parsedAbi
		}
	})
	if err != nil {
		return nil, fmt.Errorf("error parsing Polygon messenger ABI: %w", err)
	}

	// Create the contract
	contract := &core.Contract{
		Contract: bind.NewBoundContract(address, polygonMessengerAbi, client, client, client),
		Address:  &address,
		ABI:      &polygonMessengerAbi,
		Client:   client,
	}

	return &PolygonMessenger{
		contract: contract,
	}, nil
}

// =============
// === Calls ===
// =============

// Check if the RPL rate is stale and needs to be updated
func (c *PolygonMessenger) IsRateStale(mc *batch.MultiCaller, out *bool) {
	core.AddCall(mc, c.contract, out, "rateStale")
}

// ====================
// === Transactions ===
// ====================

// Send the latest RPL rate to the L2
func (c *PolygonMessenger) SubmitRate(opts *bind.TransactOpts) (*eth.TransactionInfo, error) {
	return core.NewTransactionInfo(c.contract, "submitRate", opts)
}
