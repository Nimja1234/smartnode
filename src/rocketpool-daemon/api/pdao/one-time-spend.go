package pdao

import (
	"errors"
	"fmt"
	"math/big"
	"net/url"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/mux"
	batch "github.com/rocket-pool/batch-query"
	"github.com/rocket-pool/node-manager-core/api/server"
	"github.com/rocket-pool/node-manager-core/beacon"
	"github.com/rocket-pool/node-manager-core/eth"
	"github.com/rocket-pool/node-manager-core/utils/input"
	"github.com/rocket-pool/rocketpool-go/dao/protocol"
	"github.com/rocket-pool/rocketpool-go/node"
	"github.com/rocket-pool/rocketpool-go/rocketpool"
	"github.com/rocket-pool/smartnode/shared/config"
	"github.com/rocket-pool/smartnode/shared/types/api"
)

// ===============
// === Factory ===
// ===============

type protocolDaoProposeOneTimeSpendContextFactory struct {
	handler *ProtocolDaoHandler
}

func (f *protocolDaoProposeOneTimeSpendContextFactory) Create(args url.Values) (*protocolDaoProposeOneTimeSpendContext, error) {
	c := &protocolDaoProposeOneTimeSpendContext{
		handler: f.handler,
	}
	inputErrs := []error{
		server.GetStringFromVars("invoice-id", args, &c.invoiceID),
		server.ValidateArg("recipient", args, input.ValidateAddress, &c.recipient),
		server.ValidateArg("amount", args, input.ValidateBigInt, &c.amount),
	}
	return c, errors.Join(inputErrs...)
}

func (f *protocolDaoProposeOneTimeSpendContextFactory) RegisterRoute(router *mux.Router) {
	server.RegisterSingleStageRoute[*protocolDaoProposeOneTimeSpendContext, api.ProtocolDaoGeneralProposeData](
		router, "one-time-spend", f, f.handler.serviceProvider.ServiceProvider,
	)
}

// ===============
// === Context ===
// ===============

type protocolDaoProposeOneTimeSpendContext struct {
	handler     *ProtocolDaoHandler
	rp          *rocketpool.RocketPool
	cfg         *config.SmartNodeConfig
	bc          beacon.IBeaconClient
	nodeAddress common.Address

	invoiceID string
	recipient common.Address
	amount    *big.Int
	node      *node.Node
	pdaoMgr   *protocol.ProtocolDaoManager
}

func (c *protocolDaoProposeOneTimeSpendContext) Initialize() error {
	sp := c.handler.serviceProvider
	c.rp = sp.GetRocketPool()
	c.cfg = sp.GetConfig()
	c.bc = sp.GetBeaconClient()
	c.nodeAddress, _ = sp.GetWallet().GetAddress()

	// Requirements
	err := sp.RequireNodeRegistered(c.handler.context)
	if err != nil {
		return err
	}

	// Bindings
	c.node, err = node.NewNode(c.rp, c.nodeAddress)
	if err != nil {
		return fmt.Errorf("error creating node binding: %w", err)
	}
	c.pdaoMgr, err = protocol.NewProtocolDaoManager(c.rp)
	if err != nil {
		return fmt.Errorf("error creating protocol DAO manager binding: %w", err)
	}
	return nil
}

func (c *protocolDaoProposeOneTimeSpendContext) GetState(mc *batch.MultiCaller) {
	eth.AddQueryablesToMulticall(mc,
		c.pdaoMgr.Settings.Proposals.ProposalBond,
		c.node.RplLocked,
		c.node.RplStake,
	)
}

func (c *protocolDaoProposeOneTimeSpendContext) PrepareData(data *api.ProtocolDaoGeneralProposeData, opts *bind.TransactOpts) error {
	data.StakedRpl = c.node.RplStake.Get()
	data.LockedRpl = c.node.RplLocked.Get()
	data.ProposalBond = c.pdaoMgr.Settings.Proposals.ProposalBond.Get()
	unlockedRpl := big.NewInt(0).Sub(data.StakedRpl, data.LockedRpl)
	data.InsufficientRpl = (unlockedRpl.Cmp(data.ProposalBond) < 0)
	data.CanPropose = !(data.InsufficientRpl)

	// Get the tx
	if data.CanPropose && opts != nil {
		blockNumber, pollard, err := createPollard(c.handler.context, c.rp, c.cfg, c.bc)
		if err != nil {
			return fmt.Errorf("error creating pollard for proposal creation: %w", err)
		}

		message := fmt.Sprintf("one-time spend for invoice %s", c.invoiceID)
		txInfo, err := c.pdaoMgr.ProposeOneTimeTreasurySpend(message, c.invoiceID, c.recipient, c.amount, blockNumber, pollard, opts)
		if err != nil {
			return fmt.Errorf("error getting TX info for ProposeOneTimeTreasurySpend: %w", err)
		}
		data.TxInfo = txInfo
	}
	return nil
}
