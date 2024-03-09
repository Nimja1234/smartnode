package beacon

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/prysmaticlabs/go-bitfield"
	"github.com/rocket-pool/node-manager-core/beacon"
	"github.com/rocket-pool/rocketpool-go/types"
	sharedtypes "github.com/rocket-pool/smartnode/shared/types"
)

// API request options
type ValidatorStatusOptions struct {
	Epoch *uint64
	Slot  *uint64
}

// API response types
type SyncStatus struct {
	Syncing  bool
	Progress float64
}
type Eth2Config struct {
	GenesisForkVersion           []byte
	GenesisValidatorsRoot        []byte
	GenesisEpoch                 uint64
	GenesisTime                  uint64
	SecondsPerSlot               uint64
	SlotsPerEpoch                uint64
	SecondsPerEpoch              uint64
	EpochsPerSyncCommitteePeriod uint64
}
type Eth2DepositContract struct {
	ChainID uint64
	Address common.Address
}
type BeaconHead struct {
	Epoch                  uint64
	FinalizedEpoch         uint64
	JustifiedEpoch         uint64
	PreviousJustifiedEpoch uint64
}
type ValidatorStatus struct {
	Pubkey                     beacon.ValidatorPubkey
	Index                      string
	WithdrawalCredentials      common.Hash
	Balance                    uint64
	Status                     sharedtypes.ValidatorState
	EffectiveBalance           uint64
	Slashed                    bool
	ActivationEligibilityEpoch uint64
	ActivationEpoch            uint64
	ExitEpoch                  uint64
	WithdrawableEpoch          uint64
	Exists                     bool
}
type Eth1Data struct {
	DepositRoot  common.Hash
	DepositCount uint64
	BlockHash    common.Hash
}
type BeaconBlock struct {
	Slot                 uint64
	ProposerIndex        string
	HasExecutionPayload  bool
	Attestations         []AttestationInfo
	FeeRecipient         common.Address
	ExecutionBlockNumber uint64
}

// Committees is an interface as an optimization- since committees responses
// are quite large, there's a decent cpu/memory improvement to removing the
// translation to an intermediate storage class.
//
// Instead, the interface provides the access pattern that smartnode (or more
// specifically, tree-gen) wants, and the underlying format is just the format
// of the Beacon Node response.
type Committees interface {
	// Index returns the index of the committee at the provided offset
	Index(int) uint64
	// Slot returns the slot of the committee at the provided offset
	Slot(int) uint64
	// Validators returns the list of validators of the committee at
	// the provided offset
	Validators(int) []string
	// Count returns the number of committees in the response
	Count() int
	// Release returns the reused validators slice buffer to the pool for
	// further reuse, and must be called when the user is done with this
	// committees instance
	Release()
}

type AttestationInfo struct {
	AggregationBits bitfield.Bitlist
	SlotIndex       uint64
	CommitteeIndex  uint64
}

// Beacon client interface
type Client interface {
	GetSyncStatus() (SyncStatus, error)
	GetEth2Config() (Eth2Config, error)
	GetEth2DepositContract() (Eth2DepositContract, error)
	GetAttestations(blockId string) ([]AttestationInfo, bool, error)
	GetBeaconBlock(blockId string) (BeaconBlock, bool, error)
	GetBeaconHead() (BeaconHead, error)
	GetValidatorStatusByIndex(index string, opts *ValidatorStatusOptions) (ValidatorStatus, error)
	GetValidatorStatus(pubkey beacon.ValidatorPubkey, opts *ValidatorStatusOptions) (ValidatorStatus, error)
	GetValidatorStatuses(pubkeys []beacon.ValidatorPubkey, opts *ValidatorStatusOptions) (map[beacon.ValidatorPubkey]ValidatorStatus, error)
	GetValidatorIndex(pubkey beacon.ValidatorPubkey) (string, error)
	GetValidatorSyncDuties(indices []string, epoch uint64) (map[string]bool, error)
	GetValidatorProposerDuties(indices []string, epoch uint64) (map[string]uint64, error)
	GetDomainData(domainType []byte, epoch uint64, useGenesisFork bool) ([]byte, error)
	ExitValidator(validatorIndex string, epoch uint64, signature types.ValidatorSignature) error
	Close() error
	GetEth1DataForEth2Block(blockId string) (Eth1Data, bool, error)
	GetCommitteesForEpoch(epoch *uint64) (Committees, error)
	ChangeWithdrawalCredentials(validatorIndex string, fromBlsPubkey beacon.ValidatorPubkey, toExecutionAddress common.Address, signature types.ValidatorSignature) error
}
