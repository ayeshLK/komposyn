// Copyright 2026 Ayesh Almeida
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package readbrokerstudy contains the fixed Linux broker/worker and its
// lifecycle coordinator, plus disposable feasibility fixtures. The experimental
// chat may compose the coordinator behind a fail-closed host service; this
// package does not register model-facing filesystem tools.
package readbrokerstudy

import (
	"context"
	"errors"
	"os"
	"time"
)

const (
	MaxReadBytes       = 64 << 10
	MaxPinnedObjects   = 256
	MaxPolicyLocations = 256
)

var (
	ErrInvalidPath       = errors.New("read fixture path must be clean and absolute")
	ErrOutOfScope        = errors.New("read fixture target is outside the selected root")
	ErrProtected         = errors.New("registered protected resource is unavailable to generic reads")
	ErrDirectoryCoverage = errors.New("read scope overlaps a registered protected directory with incomplete descendant identity coverage")
	ErrPolicyChanged     = errors.New("protected policy changed during read preparation")
	ErrStaleReceipt      = errors.New("authorization receipt does not match the prepared read")
	ErrPinBudget         = errors.New("protected identity pin budget exhausted")
	ErrUnavailable       = errors.New("sandboxed read worker is unavailable")
	ErrCleanupPending    = errors.New("worker termination requested; cleanup is not yet confirmed")
	ErrProtocol          = errors.New("sandboxed read worker protocol is invalid")
	ErrResultTooLarge    = errors.New("sandboxed read result exceeds its bound")
)

type ResourceClass string

const (
	ClassOrdinary      ResourceClass = "ordinary"
	ClassCredential    ResourceClass = "credential"
	ClassSettings      ResourceClass = "settings"
	ClassTokenDatabase ResourceClass = "token_database"
	ClassProvider      ResourceClass = "provider_source"
)

type FileIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	Type   uint32 `json:"type"`
}

type OwnerRegistration struct {
	Owner     string        `json:"owner"`
	Class     ResourceClass `json:"class"`
	Path      string        `json:"path"`
	Directory bool          `json:"directory"`
}

type PolicyView struct {
	Generation uint64
	Entries    []PolicyEntryView
}

type PolicyEntryView struct {
	Owner     string
	Class     ResourceClass
	Path      string
	Directory bool
	Identity  FileIdentity
}

// PolicyRegistry is host-owned. Implementations retain pinned identities until
// Close rather than evicting retired objects that active work may still use.
type PolicyRegistry interface {
	Register(OwnerRegistration) error
	RegisterRotation(owner string, next []OwnerRegistration) error
	View() PolicyView
	// ClassifyRoute applies registered-location rules without opening or
	// resolving the requested object. It lets reserved protected routes be
	// refused even while their leaf is absent or replaced by an unreadable
	// object. Identity-based policy is applied separately after open.
	ClassifyRoute(path string) (ResourceClass, bool)
	Classify(path string, identity FileIdentity) (ResourceClass, bool)
	ClassifyWithAncestors(path string, identity FileIdentity, ancestors []FileIdentity) (ResourceClass, bool)
	DirectoryCoverageIncomplete(identity FileIdentity) bool
	Close() error
}

// PreparedRead is a broker-owned capability. Its duplicate method is only for
// the fixture coordinator to hand to the fixed worker after authorization; it
// is not returned to a model tool or frontend.
type PreparedRead interface {
	Identity() FileIdentity
	Class() ResourceClass
	Path() string
	Generations() GenerationSet
	RequestDigest() [32]byte
	DuplicateForWorker() (*os.File, error)
	Close() error
}

// AdmissionRequest is trusted host context for one exact-file lookup. Root is
// the explicitly selected lookup root; Destination identifies the selected
// provider/model/profile binding for consent. Neither field grants access.
type AdmissionRequest struct {
	ConversationID string
	TurnID         string
	CallID         string
	Root           string
	Target         string
	RootGeneration uint64
	Destination    string
}

// AdmissionMetadata is detached disclosure metadata. The opaque reference is
// host-created and is validated against the owning coordinator's record map.
type AdmissionMetadata struct {
	Reference      *AdmissionReference
	ServiceID      string
	ConversationID string
	TurnID         string
	CallID         string
	Root           string
	Target         string
	RootGeneration uint64
	Deadline       time.Time
	Destination    string
	Identity       FileIdentity
	Class          ResourceClass
	Generations    GenerationSet
	Binding        [32]byte
}

// AdmissionReference cannot be constructed outside this package. Its random
// ID is only a lookup key; coordinator membership and lifecycle state remain
// the authority check.
type AdmissionReference struct {
	owner any
	id    string
}

// AdmittedPreparation is a host-owned retained object after the runtime has
// validated checked approval. It has no execution or descriptor-transfer API.
type AdmittedPreparation interface {
	Metadata() AdmissionMetadata
	WithCommit(context.Context, func() error) error
	Close(context.Context) error
}

// ReadExecutionGate is supplied only by the trusted in-process runtime. It
// never crosses the worker protocol. WithCurrent holds the runtime authority
// gate while the callback performs one short host validation/release step.
type ReadExecutionGate interface {
	Claim() error
	WithCurrent(func() error) error
}

// RetainedReadExecutor runs the already-opened object without reopening its
// pathname or consulting a second authorizer. capture is invoked only after
// the worker has joined and all operation-owned descriptors have been closed.
type RetainedReadExecutor interface {
	ExecuteAdmitted(context.Context, AdmittedPreparation, ReadExecutionGate, func(AdmissionMetadata, ReadCompletion) error) error
}

type ConversationReadInvalidator interface {
	InvalidateConversationReads(context.Context, string) error
}

type ReadCompletion struct {
	Result              WorkerResult
	WorkerJoined        bool
	CapabilitiesRetired bool
	ReadStarted         bool
}

type ReadBroker interface {
	PrepareRead(root, target string, generation GenerationSet) (PreparedRead, error)
	Revalidate(PreparedRead) error
}

type GenerationSet struct {
	Root      uint64 `json:"root_generation"`
	Policy    uint64 `json:"policy_generation"`
	Grant     uint64 `json:"grant_generation"`
	Inventory uint64 `json:"inventory_revision"`
}

type AuthorizationReceipt struct {
	InvocationID  string        `json:"invocation_id"`
	Identity      FileIdentity  `json:"identity"`
	Class         ResourceClass `json:"class"`
	Generations   GenerationSet `json:"generations"`
	OnceConsumed  bool          `json:"once_consumed"`
	RequestDigest [32]byte      `json:"request_digest"`
}

type AuthorizationRequest struct {
	InvocationID  string
	Identity      FileIdentity
	Class         ResourceClass
	Generations   GenerationSet
	RequestDigest [32]byte
}

// FixtureAuthorizer owns the single once-grant consumption in this harness.
// A successful call consumes exactly once before any descriptor is handed to
// the sandbox worker.
type FixtureAuthorizer interface {
	AuthorizeAndConsume(context.Context, AuthorizationRequest) (AuthorizationReceipt, error)
}

type ReadOutcome string

const (
	ReadNotExecuted ReadOutcome = "not_executed"
	ReadSucceeded   ReadOutcome = "succeeded"
	ReadRefused     ReadOutcome = "refused"
	ReadFailed      ReadOutcome = "failed"
	ReadPartial     ReadOutcome = "partial"
	ReadUnknown     ReadOutcome = "unknown"
	ReadCancelled   ReadOutcome = "cancelled"
)

type OperationState string

const (
	StatePrepared       OperationState = "prepared"
	StateLaunching      OperationState = "launching"
	StateReady          OperationState = "ready"
	StateCapabilityHeld OperationState = "capability_held"
	StateRunning        OperationState = "running"
	StateTerminating    OperationState = "terminating"
	StateJoined         OperationState = "joined"
)

type Checkpoint string

const (
	CheckpointBeforeOpen          Checkpoint = "before_open"
	CheckpointAfterOpen           Checkpoint = "after_open"
	CheckpointAfterClassify       Checkpoint = "after_classify"
	CheckpointBeforeHandoff       Checkpoint = "before_handoff"
	CheckpointAfterCapabilityHeld Checkpoint = "after_capability_held_before_read_release"
	// CheckpointAfterFinalRevalidation is the last deterministic fixture hook
	// after the broker's final policy scan and before duplicating or releasing
	// the read capability. Native actor scenarios use it to expose the
	// check-to-handoff namespace window.
	CheckpointAfterFinalRevalidation Checkpoint = "after_final_revalidation_before_handoff"
	CheckpointAfterCapture           Checkpoint = "after_capture_before_publish"
	CheckpointBeforePublish          Checkpoint = "before_publish"
)

type ReadEvidence struct {
	Case               string        `json:"case"`
	Outcome            ReadOutcome   `json:"outcome"`
	FailureDetail      string        `json:"failure_detail,omitempty"`
	Generations        GenerationSet `json:"generations"`
	Probe              WorkerProbe   `json:"worker_probe,omitempty"`
	WorkerStarted      bool          `json:"worker_started"`
	WorkerReady        bool          `json:"worker_ready"`
	WorkerJoined       bool          `json:"worker_join_confirmed"`
	CapabilitySent     bool          `json:"capability_sent"`
	CapabilityHeld     bool          `json:"capability_received_held"`
	CapabilityIdentity FileIdentity  `json:"capability_identity,omitempty"`
	ReadStarted        bool          `json:"read_started"`
	Published          bool          `json:"published"`
	BytesMatchTarget   bool          `json:"bytes_match_target"`
	Effect             string        `json:"fixture_effect"`
	Limitation         string        `json:"limitation,omitempty"`
}

type CheckpointHook func(Checkpoint) error

// HostCoordinator couples one-time authorization, worker launch/release,
// generation invalidation, and publication. The caller supplies the trusted
// authorizer and coverage profile; tool/model inputs cannot construct either.
type HostCoordinator interface {
	Read(context.Context, string, string, string) (WorkerResult, error)
	Admit(context.Context, AdmissionRequest) (AdmissionMetadata, error)
	BeginAdmission(context.Context, AdmissionRequest) (*AdmissionReference, error)
	WaitAdmission(context.Context, *AdmissionReference) (AdmissionMetadata, error)
	PrepareAdmitted(context.Context, *AdmissionReference, string, uint64) (AdmittedPreparation, error)
	CloseAdmission(context.Context, *AdmissionReference) error
	InvalidateAdmissions(context.Context) error
	AdmissionConversations() []string
	DeclareCoverageOwner(OwnerRequirement) error
	RetireCoverageOwner(string) error
	Register(OwnerRegistration) error
	RegisterRotation(owner string, next []OwnerRegistration) error
	RegisterRotationContext(context.Context, string, []OwnerRegistration) error
	SelectRoot(root string) error
	SelectRootGeneration(context.Context, string, uint64) error
	Revoke(invocationID string) error
	SetCheckpointHook(CheckpointHook)
	PublishedResults() [][]byte
	Evidence() []ReadEvidence
	GenerationSnapshot() GenerationSet
	CoverageSnapshot() CoverageSnapshot
	ActiveOperationCount() int
	Close(context.Context) error
}

// FixtureSession is retained for the disposable study command and its
// evidence-oriented test hooks.
type FixtureSession interface {
	HostCoordinator
}

type WorkerResult struct {
	Outcome   ReadOutcome `json:"outcome"`
	Bytes     []byte      `json:"bytes,omitempty"`
	Detail    string      `json:"detail,omitempty"`
	Truncated bool        `json:"truncated,omitempty"`
}

type WorkerProbe struct {
	NoNewPrivs                    bool     `json:"no_new_privs"`
	SeccompMode                   int      `json:"seccomp_mode"`
	SocketCreationDenied          bool     `json:"socket_creation_denied"`
	PathOpenDenied                bool     `json:"path_open_denied"`
	ReadCapabilityInitiallyAbsent bool     `json:"read_capability_initially_absent"`
	OnlyProtocolDescriptor        bool     `json:"only_protocol_descriptor"`
	EnvironmentCleared            bool     `json:"environment_cleared"`
	Details                       []string `json:"details,omitempty"`
}

type SandboxWorker interface {
	Ready(context.Context) error
	Probe() WorkerProbe
	TransferCapability(context.Context, *os.File, FileIdentity) error
	AwaitReadStarted(context.Context) error
	ReadResult(context.Context) (WorkerResult, error)
	Terminate() error
	// Wait returns the direct process's exit status/error; an error alone does
	// not indicate that it remains unjoined.
	Wait() error
	// Joined confirms that the direct process was waited/reaped. It does not
	// claim that every namespace descendant has terminated.
	Joined() bool
}

// HostProcessIdentity names one task-owned process by PID plus its kernel
// start-time token. It is for the trusted study supervisor only; never pass it
// into the sandbox worker or publish the raw PID in a shareable report.
type HostProcessIdentity struct {
	PID            int    `json:"pid"`
	StartTimeTicks uint64 `json:"start_time_ticks"`
}

// HostProcessObserver is an optional research-only observation surface. The
// implementation reads the host's procfs from the trusted coordinator and
// must not add a procfs mount or authority to the worker.
type HostProcessObserver interface {
	ObserveHostProcesses() (bubblewrap HostProcessIdentity, namespaceInit HostProcessIdentity, err error)
}

// HeldCapabilityObserver exposes the identity already checked by the fixed
// worker's CAPABILITY_HELD acknowledgement. It is research evidence only.
type HeldCapabilityObserver interface {
	HeldCapabilityIdentity() (FileIdentity, bool)
}

// HeldCapabilityWorker is implemented only by the fixed study worker when
// launched in its bounded owner-lifecycle mode. It acknowledges receipt and
// identity validation of the read-only FD, then waits until ReleaseHeldRead or
// worker termination. It is not an arbitrary worker control API.
type HeldCapabilityWorker interface {
	AwaitCapabilityHeld(context.Context) error
	ReleaseHeldRead(context.Context) error
}

type WorkerLauncher interface {
	Start(context.Context) (SandboxWorker, error)
	Close() error
}
