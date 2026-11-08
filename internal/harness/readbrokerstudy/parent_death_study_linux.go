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

//go:build linux && komposyn_stage6_study

package readbrokerstudy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// ParentDeathStudyEvent is a bounded local handshake. Process identities are
// consumed by the supervisor and must not be copied into the shareable report.
type ParentDeathStudyEvent struct {
	Phase         string              `json:"phase"`
	Bubblewrap    HostProcessIdentity `json:"bubblewrap,omitempty"`
	NamespaceInit HostProcessIdentity `json:"namespace_init,omitempty"`
	WorkerJoined  bool                `json:"worker_joined,omitempty"`
}

// RunParentDeathStudyCoordinator starts the fixed held-read worker and writes
// the identities of Bubblewrap and its PID-namespace init only after startup
// probes and the capability-held acknowledgement. The caller intentionally
// kills this coordinator to measure --die-with-parent; no read-release frame
// is sent.
func RunParentDeathStudyCoordinator(ctx context.Context, bwrapPath, targetPath string, reportWriter io.Writer) error {
	if ctx == nil || reportWriter == nil || bwrapPath == "" || targetPath == "" {
		return errors.New("parent-death study inputs are incomplete")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	workerPath, err := os.Executable()
	if err != nil {
		return errors.New("parent-death study executable is unavailable")
	}
	launcher, err := NewWorkerLauncherWithCapabilityHold(bwrapPath, workerPath)
	if err != nil {
		return err
	}
	defer launcher.Close()
	worker, err := launcher.Start(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if !worker.Joined() {
			_ = worker.Terminate()
			_ = worker.Wait()
		}
		_ = writeParentDeathStudyEvent(reportWriter, ParentDeathStudyEvent{Phase: "worker_cleanup", WorkerJoined: worker.Joined()})
	}()
	if err := writeParentDeathStudyEvent(reportWriter, ParentDeathStudyEvent{Phase: "worker_started"}); err != nil {
		return err
	}
	if err := worker.Ready(ctx); err != nil {
		return err
	}
	if err := writeParentDeathStudyEvent(reportWriter, ParentDeathStudyEvent{Phase: "worker_ready"}); err != nil {
		return err
	}
	file, err := os.Open(targetPath)
	if err != nil {
		return errors.New("synthetic parent-death target could not be opened")
	}
	defer file.Close()
	identity, err := identityFromFile(file)
	if err != nil {
		return errors.New("synthetic parent-death target identity is unavailable")
	}
	if err := worker.TransferCapability(ctx, file, identity); err != nil {
		return err
	}
	held, ok := worker.(HeldCapabilityWorker)
	if !ok {
		return errors.New("fixed worker has no held-capability protocol")
	}
	if err := held.AwaitCapabilityHeld(ctx); err != nil {
		return err
	}
	if err := writeParentDeathStudyEvent(reportWriter, ParentDeathStudyEvent{Phase: "capability_held"}); err != nil {
		return err
	}
	observer, ok := worker.(HostProcessObserver)
	if !ok {
		return errors.New("fixed worker has no host process observer")
	}
	bubblewrap, namespaceInit, err := observer.ObserveHostProcesses()
	if err != nil {
		return err
	}
	if err := writeParentDeathStudyEvent(reportWriter, ParentDeathStudyEvent{
		Phase: "processes_observed", Bubblewrap: bubblewrap, NamespaceInit: namespaceInit,
	}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func writeParentDeathStudyEvent(writer io.Writer, event ParentDeathStudyEvent) error {
	if err := json.NewEncoder(writer).Encode(event); err != nil {
		return errors.New("parent-death study handshake could not be written")
	}
	return nil
}
