package minipool

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	batch "github.com/rocket-pool/batch-query"
	"github.com/rocket-pool/rocketpool-go/core"
	"github.com/rocket-pool/rocketpool-go/minipool"
	"github.com/rocket-pool/rocketpool-go/node"
	"github.com/rocket-pool/rocketpool-go/rocketpool"
	"github.com/rocket-pool/rocketpool-go/settings"
	"github.com/rocket-pool/rocketpool-go/types"
	"github.com/rocket-pool/smartnode/shared/services/beacon"
	"github.com/rocket-pool/smartnode/shared/types/api"
	"github.com/urfave/cli"
)

type minipoolReduceBondManager struct {
	pSettings *settings.ProtocolDaoSettings
	oSettings *settings.OracleDaoSettings
}

func (m *minipoolReduceBondManager) CreateBindings(rp *rocketpool.RocketPool) error {
	var err error
	m.pSettings, err = settings.NewProtocolDaoSettings(rp)
	if err != nil {
		return fmt.Errorf("error creating pDAO settings binding: %w", err)
	}
	m.oSettings, err = settings.NewOracleDaoSettings(rp)
	if err != nil {
		return fmt.Errorf("error creating oDAO settings binding: %w", err)
	}
	return nil
}

func (m *minipoolReduceBondManager) GetState(node *node.Node, mc *batch.MultiCaller) {
	m.pSettings.GetBondReductionEnabled(mc)
	m.oSettings.GetBondReductionWindowStart(mc)
	m.oSettings.GetBondReductionWindowLength(mc)
}

func (m *minipoolReduceBondManager) CheckState(node *node.Node, response *api.MinipoolReduceBondDetailsResponse) bool {
	response.BondReductionDisabled = !m.pSettings.Details.Minipool.IsBondReductionEnabled
	return !response.BondReductionDisabled
}

func (m *minipoolReduceBondManager) GetMinipoolDetails(mc *batch.MultiCaller, mp minipool.Minipool, index int) {
	mpv3, success := minipool.GetMinipoolAsV3(mp)
	if success {
		mpv3.GetFinalised(mc)
		mpv3.GetStatus(mc)
		mpv3.GetPubkey(mc)
		mpv3.GetReduceBondTime(mc)
	}
}

func (m *minipoolReduceBondManager) PrepareResponse(rp *rocketpool.RocketPool, bc beacon.Client, addresses []common.Address, mps []minipool.Minipool, response *api.MinipoolReduceBondDetailsResponse) error {
	// Get the latest block header
	header, err := rp.Client.HeaderByNumber(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("error getting latest block header: %w", err)
	}
	currentTime := time.Unix(int64(header.Time), 0)

	// Get the bond reduction details
	pubkeys := []types.ValidatorPubkey{}
	detailsMap := map[types.ValidatorPubkey]int{}
	details := make([]api.MinipoolReduceBondDetails, len(addresses))
	for i, mp := range mps {
		mpCommon := mp.GetMinipoolCommon()
		mpDetails := api.MinipoolReduceBondDetails{
			Address: mpCommon.Details.Address,
		}

		mpv3, success := minipool.GetMinipoolAsV3(mp)
		if !success {
			mpDetails.MinipoolVersionTooLow = true
		} else if mpCommon.Details.Status.Formatted() != types.Staking || mpCommon.Details.IsFinalised {
			mpDetails.InvalidElState = true
		} else {
			reductionStart := mpv3.Details.ReduceBondTime.Formatted()
			timeSinceBondReductionStart := currentTime.Sub(reductionStart)
			windowStart := m.oSettings.Details.Minipools.BondReductionWindowStart.Formatted()
			windowEnd := windowStart + m.oSettings.Details.Minipools.BondReductionWindowLength.Formatted()

			if timeSinceBondReductionStart < windowStart || timeSinceBondReductionStart > windowEnd {
				mpDetails.OutOfWindow = true
			} else {
				pubkeys = append(pubkeys, mpCommon.Details.Pubkey)
				detailsMap[mpCommon.Details.Pubkey] = i
			}
		}

		details[i] = mpDetails
	}

	// Get the statuses on Beacon
	beaconStatuses, err := bc.GetValidatorStatuses(pubkeys, nil)
	if err != nil {
		return fmt.Errorf("error getting validator statuses on Beacon: %w", err)
	}

	// Do a complete viability check
	for pubkey, beaconStatus := range beaconStatuses {
		i := detailsMap[pubkey]
		mpDetails := &details[i]
		mpDetails.Balance = beaconStatus.Balance
		mpDetails.BeaconState = beaconStatus.Status

		// Check the beacon state
		mpDetails.InvalidBeaconState = !(mpDetails.BeaconState == beacon.ValidatorState_PendingInitialized ||
			mpDetails.BeaconState == beacon.ValidatorState_PendingQueued ||
			mpDetails.BeaconState == beacon.ValidatorState_ActiveOngoing)

		// Make sure the balance is high enough
		threshold := uint64(32000000000)
		mpDetails.BalanceTooLow = mpDetails.Balance < threshold

		mpDetails.CanReduce = !(response.BondReductionDisabled || mpDetails.MinipoolVersionTooLow || mpDetails.OutOfWindow || mpDetails.BalanceTooLow || mpDetails.InvalidBeaconState)
	}

	response.Details = details
	return nil
}

func reduceBondAmounts(c *cli.Context, minipoolAddresses []common.Address) (*api.BatchTxInfoData, error) {
	return createBatchTxResponseForV3(c, minipoolAddresses, func(mpv3 *minipool.MinipoolV3, opts *bind.TransactOpts) (*core.TransactionInfo, error) {
		return mpv3.ReduceBondAmount(opts)
	}, "reduce-bond")
}
