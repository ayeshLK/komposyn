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

// Package readbrokerstudy contains a disposable feasibility harness for
// brokered, sandboxed typed reads. It is not used by production dispatch and
// does not register filesystem tools.
package readbrokerstudy

import (
	"context"
	"errors"
	"os"
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
	Classify(path string, identity FileIdentity) (ResourceClass, bool)
	ClassifyWithAncestors(path string, identity FileIdentity, ancestors []FileIdentity) (ResourceClass, bool)
	InspectRegisteredLocations(identity FileIdentity) (ResourceClass, bool, error)
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

type ReadBroker interface {
	PrepareRead(root, target string, generation GenerationSet) (PreparedRead, error)
	Revalidate(PreparedRead) error
}

type GenerationSet struct {
	Root   uint64 `json:"root_generation"`
	Policy uint64 `json:"policy_generation"`
	Grant  uint64 `json:"grant_generation"`
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
	StatePrepared    OperationState = "prepared"
	StateLaunching   OperationState = "launching"
	StateReady       OperationState = "ready"
	StateRunning     OperationState = "running"
	StateTerminating OperationState = "terminating"
	StateJoined      OperationState = "joined"
)

type Checkpoint string

const (
	CheckpointBeforeOpen    Checkpoint = "before_open"
	CheckpointAfterOpen     Checkpoint = "after_open"
	CheckpointAfterClassify Checkpoint = "after_classify"
	CheckpointBeforeHandoff Checkpoint = "before_handoff"
	CheckpointAfterCapture  Checkpoint = "after_capture_before_publish"
	CheckpointBeforePublish Checkpoint = "before_publish"
)

type ReadEvidence struct {
	Case             string        `json:"case"`
	Outcome          ReadOutcome   `json:"outcome"`
	Generations      GenerationSet `json:"generations"`
	Probe            WorkerProbe   `json:"worker_probe,omitempty"`
	WorkerStarted    bool          `json:"worker_started"`
	CapabilitySent   bool          `json:"capability_sent"`
	ReadStarted      bool          `json:"read_started"`
	Published        bool          `json:"published"`
	BytesMatchTarget bool          `json:"bytes_match_target"`
	Effect           string        `json:"fixture_effect"`
	Limitation       string        `json:"limitation,omitempty"`
}

type CheckpointHook func(Checkpoint) error

// FixtureSession is coordinator-only test composition. It couples one-time
// authorization consumption, worker launch/release, generation invalidation,
// and publication. Production runtime code does not use this type.
type FixtureSession interface {
	Read(context.Context, string, string, string) (WorkerResult, error)
	Register(OwnerRegistration) error
	RegisterRotation(owner string, next []OwnerRegistration) error
	SelectRoot(root string) error
	Revoke(invocationID string) error
	SetCheckpointHook(CheckpointHook)
	PublishedResults() [][]byte
	Evidence() []ReadEvidence
	Close(context.Context) error
}

type WorkerResult struct {
	Outcome ReadOutcome `json:"outcome"`
	Bytes   []byte      `json:"bytes,omitempty"`
	Detail  string      `json:"detail,omitempty"`
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

type WorkerLauncher interface {
	Start(context.Context) (SandboxWorker, error)
	Close() error
}
