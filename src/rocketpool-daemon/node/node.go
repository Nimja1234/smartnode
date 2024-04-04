package node

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/rocket-pool/node-manager-core/log"
	"github.com/rocket-pool/node-manager-core/utils"
	"github.com/rocket-pool/smartnode/rocketpool-daemon/common/alerting"
	"github.com/rocket-pool/smartnode/rocketpool-daemon/common/services"
	"github.com/rocket-pool/smartnode/rocketpool-daemon/common/state"
	"github.com/rocket-pool/smartnode/rocketpool-daemon/node/collectors"
	"github.com/rocket-pool/smartnode/shared/config"
)

// Config
const (
	tasksInterval               time.Duration = time.Minute * 5
	taskCooldown                time.Duration = time.Second * 10
	totalEffectiveStakeCooldown time.Duration = time.Hour * 1
	metricsShutdownTimeout      time.Duration = time.Second * 5
)

type TaskLoop struct {
	logger        *log.Logger
	ctx           context.Context
	sp            *services.ServiceProvider
	wg            *sync.WaitGroup
	metricsServer *http.Server
}

func NewTaskLoop(sp *services.ServiceProvider, wg *sync.WaitGroup) *TaskLoop {
	logger := sp.GetTasksLogger()
	return &TaskLoop{
		sp:     sp,
		logger: logger,
		ctx:    logger.CreateContextWithLogger(sp.GetBaseContext()),
		wg:     wg,
	}
}

// Run daemon
func (t *TaskLoop) Run() error {
	// Get services
	cfg := t.sp.GetConfig()
	rp := t.sp.GetRocketPool()
	ec := t.sp.GetEthClient()
	bc := t.sp.GetBeaconClient()

	// Print the current mode
	if cfg.IsNativeMode {
		fmt.Println("Starting node daemon in Native Mode.")
	} else {
		fmt.Println("Starting node daemon in Docker Mode.")
	}

	// Handle the initial fee recipient file deployment
	err := deployDefaultFeeRecipientFile(cfg)
	if err != nil {
		return err
	}

	// Create the state manager
	m, err := state.NewNetworkStateManager(t.ctx, rp, cfg, ec, bc, t.logger.Logger)
	if err != nil {
		return err
	}
	stateLocker := collectors.NewStateLocker()

	// Initialize tasks
	manageFeeRecipient := NewManageFeeRecipient(t.ctx, t.sp, t.logger)
	distributeMinipools := NewDistributeMinipools(t.sp, t.logger)
	stakePrelaunchMinipools := NewStakePrelaunchMinipools(t.sp, t.logger)
	promoteMinipools := NewPromoteMinipools(t.sp, t.logger)
	downloadRewardsTrees := NewDownloadRewardsTrees(t.sp, t.logger)
	reduceBonds := NewReduceBonds(t.sp, t.logger)
	defendPdaoProps := NewDefendPdaoProps(t.ctx, t.sp, t.logger)
	var verifyPdaoProps *VerifyPdaoProps

	// Make sure the user opted into this duty
	verifyEnabled := cfg.VerifyProposals.Value
	if verifyEnabled {
		verifyPdaoProps = NewVerifyPdaoProps(t.ctx, t.sp, t.logger)
		if err != nil {
			return err
		}
	}

	// Timestamp for caching total effective RPL stake
	lastTotalEffectiveStakeTime := time.Unix(0, 0)

	// Run task loop
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()

		// Wait until node is registered
		err := t.sp.WaitNodeRegistered(t.ctx, true)
		if err != nil {
			errMsg := err.Error()
			if !strings.Contains(errMsg, "context canceled") {
				t.logger.Error("Error waiting for node registration", slog.String(log.ErrorKey, errMsg))
			}
			return
		}

		// we assume clients are synced on startup so that we don't send unnecessary alerts
		wasExecutionClientSynced := true
		wasBeaconClientSynced := true
		for {
			// Check the EC status
			err := t.sp.WaitEthClientSynced(t.ctx, false) // Force refresh the primary / fallback EC status
			if err != nil {
				errMsg := err.Error()
				if strings.Contains(errMsg, "context canceled") {
					break
				}
				wasExecutionClientSynced = false
				t.logger.Error("Execution Client not synced. Waiting for sync...", slog.String(log.ErrorKey, errMsg))
				if utils.SleepWithCancel(t.ctx, taskCooldown) {
					break
				}
				continue
			}

			if !wasExecutionClientSynced {
				t.logger.Info("Execution Client is now synced.")
				wasExecutionClientSynced = true
				alerting.AlertExecutionClientSyncComplete(cfg)
			}

			// Check the BC status
			err = t.sp.WaitBeaconClientSynced(t.ctx, false) // Force refresh the primary / fallback BC status
			if err != nil {
				errMsg := err.Error()
				if strings.Contains(errMsg, "context canceled") {
					break
				}
				// NOTE: if not synced, it returns an error - so there isn't necessarily an underlying issue
				wasBeaconClientSynced = false
				t.logger.Error("Beacon Node not synced. Waiting for sync...", slog.String(log.ErrorKey, errMsg))
				if utils.SleepWithCancel(t.ctx, taskCooldown) {
					break
				}
				continue
			}

			if !wasBeaconClientSynced {
				t.logger.Info("Beacon Node is now synced.")
				wasBeaconClientSynced = true
				alerting.AlertBeaconClientSyncComplete(cfg)
			}

			// Load contracts
			err = t.sp.RefreshRocketPoolContracts()
			if err != nil {
				t.logger.Error("Error loading contract bindings", log.Err(err))
				if utils.SleepWithCancel(t.ctx, taskCooldown) {
					break
				}
				continue
			}

			// Update the network state
			updateTotalEffectiveStake := false
			if time.Since(lastTotalEffectiveStakeTime) > totalEffectiveStakeCooldown {
				updateTotalEffectiveStake = true
				lastTotalEffectiveStakeTime = time.Now() // Even if the call below errors out, this will prevent contant errors related to this flag
			}
			nodeAddress, hasNodeAddress := t.sp.GetWallet().GetAddress()
			if !hasNodeAddress {
				continue
			}
			state, totalEffectiveStake, err := updateNetworkState(t.ctx, m, t.logger, nodeAddress, updateTotalEffectiveStake)
			if err != nil {
				t.logger.Error(err.Error())
				if utils.SleepWithCancel(t.ctx, taskCooldown) {
					break
				}
				continue
			}
			stateLocker.UpdateState(state, totalEffectiveStake)

			// Manage the fee recipient for the node
			if err := manageFeeRecipient.Run(state); err != nil {
				t.logger.Error(err.Error())
			}
			if utils.SleepWithCancel(t.ctx, taskCooldown) {
				break
			}

			// Run the rewards download check
			if err := downloadRewardsTrees.Run(state); err != nil {
				t.logger.Error(err.Error())
			}
			if utils.SleepWithCancel(t.ctx, taskCooldown) {
				break
			}

			// Run the pDAO proposal defender
			if err := defendPdaoProps.Run(state); err != nil {
				t.logger.Error(err.Error())
			}
			if utils.SleepWithCancel(t.ctx, taskCooldown) {
				break
			}

			// Run the pDAO proposal verifier
			if verifyPdaoProps != nil {
				if err := verifyPdaoProps.Run(state); err != nil {
					t.logger.Error(err.Error())
				}
				if utils.SleepWithCancel(t.ctx, taskCooldown) {
					break
				}
			}

			// Run the minipool stake check
			if err := stakePrelaunchMinipools.Run(state); err != nil {
				t.logger.Error(err.Error())
			}
			if utils.SleepWithCancel(t.ctx, taskCooldown) {
				break
			}

			// Run the balance distribution check
			if err := distributeMinipools.Run(state); err != nil {
				t.logger.Error(err.Error())
			}
			if utils.SleepWithCancel(t.ctx, taskCooldown) {
				break
			}

			// Run the reduce bond check
			if err := reduceBonds.Run(state); err != nil {
				t.logger.Error(err.Error())
			}
			if utils.SleepWithCancel(t.ctx, taskCooldown) {
				break
			}

			// Run the minipool promotion check
			if err := promoteMinipools.Run(state); err != nil {
				t.logger.Error(err.Error())
			}

			if utils.SleepWithCancel(t.ctx, tasksInterval) {
				break
			}
		}
	}()

	// Run metrics loop
	t.metricsServer = runMetricsServer(t.ctx, t.sp, t.logger, stateLocker, t.wg)

	return nil
}

// Copy the default fee recipient file into the proper location
func deployDefaultFeeRecipientFile(cfg *config.SmartNodeConfig) error {
	feeRecipientPath := cfg.GetFeeRecipientFilePath()
	_, err := os.Stat(feeRecipientPath)
	if os.IsNotExist(err) {
		// Make sure the validators dir is created
		validatorsFolder := filepath.Dir(feeRecipientPath)
		err = os.MkdirAll(validatorsFolder, 0755)
		if err != nil {
			return fmt.Errorf("could not create validators directory: %w", err)
		}

		// Create the file
		rs := cfg.GetRocketPoolResources()
		var defaultFeeRecipientFileContents string
		if cfg.IsNativeMode {
			// Native mode needs an environment variable definition
			defaultFeeRecipientFileContents = fmt.Sprintf("FEE_RECIPIENT=%s", rs.RethAddress.Hex())
		} else {
			// Docker and Hybrid just need the address itself
			defaultFeeRecipientFileContents = rs.RethAddress.Hex()
		}
		err := os.WriteFile(feeRecipientPath, []byte(defaultFeeRecipientFileContents), 0664)
		if err != nil {
			return fmt.Errorf("could not write default fee recipient file to %s: %w", feeRecipientPath, err)
		}
	} else if err != nil {
		return fmt.Errorf("error checking fee recipient file status: %w", err)
	}

	return nil
}

func (t *TaskLoop) Stop() {
	if t.metricsServer != nil {
		// Shut down the metrics server
		ctx, cancel := context.WithTimeout(context.Background(), metricsShutdownTimeout)
		defer cancel()
		t.metricsServer.Shutdown(ctx)
	}
}

// Update the latest network state at each cycle
func updateNetworkState(ctx context.Context, m *state.NetworkStateManager, log *log.Logger, nodeAddress common.Address, calculateTotalEffectiveStake bool) (*state.NetworkState, *big.Int, error) {
	// Get the state of the network
	state, totalEffectiveStake, err := m.GetHeadStateForNode(ctx, nodeAddress, calculateTotalEffectiveStake)
	if err != nil {
		return nil, nil, fmt.Errorf("error updating network state: %w", err)
	}
	return state, totalEffectiveStake, nil
}
