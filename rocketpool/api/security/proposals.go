package security

import (
	"fmt"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/mux"
	batch "github.com/rocket-pool/batch-query"
	"github.com/rocket-pool/rocketpool-go/dao/proposals"
	"github.com/rocket-pool/rocketpool-go/dao/protocol"
	"github.com/rocket-pool/rocketpool-go/dao/security"
	"github.com/rocket-pool/rocketpool-go/rocketpool"

	"github.com/rocket-pool/smartnode/rocketpool/common/server"
	"github.com/rocket-pool/smartnode/shared/types/api"
)

const (
	proposalBatchSize int = 100
)

// ===============
// === Factory ===
// ===============

type securityProposalsContextFactory struct {
	handler *SecurityCouncilHandler
}

func (f *securityProposalsContextFactory) Create(vars map[string]string) (*securityProposalsContext, error) {
	c := &securityProposalsContext{
		handler: f.handler,
	}
	return c, nil
}

func (f *securityProposalsContextFactory) RegisterRoute(router *mux.Router) {
	server.RegisterSingleStageRoute[*securityProposalsContext, api.SecurityProposalsData](
		router, "proposals", f, f.handler.serviceProvider,
	)
}

// ===============
// === Context ===
// ===============

type securityProposalsContext struct {
	handler     *SecurityCouncilHandler
	rp          *rocketpool.RocketPool
	nodeAddress common.Address
	hasAddress  bool

	scMgr *security.SecurityCouncilManager
	dpm   *proposals.DaoProposalManager
}

func (c *securityProposalsContext) Initialize() error {
	sp := c.handler.serviceProvider
	c.rp = sp.GetRocketPool()
	c.nodeAddress, c.hasAddress = sp.GetWallet().GetAddress()

	// Requirements
	err := sp.RequireEthClientSynced()
	if err != nil {
		return err
	}

	// Bindings
	pdaoMgr, err := protocol.NewProtocolDaoManager(c.rp)
	if err != nil {
		return fmt.Errorf("error creating Protocol DAO manager binding: %w", err)
	}
	c.scMgr, err = security.NewSecurityCouncilManager(c.rp, pdaoMgr.Settings)
	if err != nil {
		return fmt.Errorf("error creating security council manager binding: %w", err)
	}
	c.dpm, err = proposals.NewDaoProposalManager(c.rp)
	if err != nil {
		return fmt.Errorf("error creating DAO proposal manager binding: %w", err)
	}
	return nil
}

func (c *securityProposalsContext) GetState(mc *batch.MultiCaller) {
	c.dpm.ProposalCount.AddToQuery(mc)
}

func (c *securityProposalsContext) PrepareData(data *api.SecurityProposalsData, opts *bind.TransactOpts) error {
	_, scProps, err := c.dpm.GetProposals(c.dpm.ProposalCount.Formatted(), true, nil)
	if err != nil {
		return fmt.Errorf("error getting proposals: %w", err)
	}

	// Get the basic details
	for _, scProp := range scProps {
		prop := api.SecurityProposalDetails{
			ID:              scProp.ID,
			ProposerAddress: scProp.ProposerAddress.Get(),
			Message:         scProp.Message.Get(),
			CreatedTime:     scProp.CreatedTime.Formatted(),
			StartTime:       scProp.StartTime.Formatted(),
			EndTime:         scProp.EndTime.Formatted(),
			ExpiryTime:      scProp.ExpiryTime.Formatted(),
			VotesRequired:   scProp.VotesRequired.Formatted(),
			VotesFor:        scProp.VotesFor.Formatted(),
			VotesAgainst:    scProp.VotesAgainst.Formatted(),
			IsCancelled:     scProp.IsCancelled.Get(),
			IsExecuted:      scProp.IsExecuted.Get(),
			Payload:         scProp.Payload.Get(),
		}
		prop.PayloadStr, err = scProp.GetPayloadAsString()
		if err != nil {
			prop.PayloadStr = fmt.Sprintf("<error decoding payload: %s>", err.Error())
		}
		data.Proposals = append(data.Proposals, prop)
	}

	// Get the node-specific details
	if c.hasAddress {
		err = c.rp.BatchQuery(len(data.Proposals), proposalBatchSize, func(mc *batch.MultiCaller, i int) error {
			odaoProp := scProps[i]
			odaoProp.GetMemberHasVoted(mc, &data.Proposals[i].MemberVoted, c.nodeAddress)
			odaoProp.GetMemberSupported(mc, &data.Proposals[i].MemberSupported, c.nodeAddress)
			return nil
		}, nil)
		if err != nil {
			return fmt.Errorf("error getting node vote status on proposals: %w", err)
		}
	}
	return nil
}
