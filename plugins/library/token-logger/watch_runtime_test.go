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

package tokenlogger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCollectorSourceRegistryCanonicalizesAndDisables(t *testing.T) {
	store := openTestStore(t)
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := store.RegisterCollectorSource(source, true)
	if err != nil || !first.Changed {
		t.Fatalf("first registration = %+v, err=%v", first, err)
	}
	second, err := store.RegisterCollectorSource(filepath.Join(root, ".", "source"), true)
	if err != nil || second.Changed || second.Source.ID != first.Source.ID {
		t.Fatalf("duplicate registration = %+v, err=%v", second, err)
	}
	removed, changed, err := store.RemoveCollectorSource(first.Source.ID)
	if err != nil || !changed || removed.ID != first.Source.ID {
		t.Fatalf("remove = %+v changed=%t err=%v", removed, changed, err)
	}
	_, changed, err = store.RemoveCollectorSource(source)
	if err != nil || changed {
		t.Fatalf("repeat remove changed=%t err=%v", changed, err)
	}
	sources, err := store.ListCollectorSources(false)
	if err != nil || len(sources) != 1 || sources[0].Enabled {
		t.Fatalf("sources = %+v err=%v", sources, err)
	}
}

func TestCollectorSourceRegistryResolvesSymlinkDuplicates(t *testing.T) {
	store := openTestStore(t)
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	first, err := store.RegisterCollectorSource(target, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.RegisterCollectorSource(link, true)
	if err != nil || second.Changed || second.Source.ID != first.Source.ID {
		t.Fatalf("symlink duplicate = %+v err=%v", second, err)
	}
}

func TestCollectorLeaseIsSingletonAndCooperativelyStops(t *testing.T) {
	store := openTestStore(t)
	interval := time.Second
	if recovered, err := store.AcquireCollectorLease("instance-a", 101, "v1", "background", interval, time.Minute); err != nil || recovered {
		t.Fatalf("acquire recovered=%t err=%v", recovered, err)
	}
	if _, err := store.AcquireCollectorLease("instance-b", 202, "v1", "background", interval, time.Minute); !errors.Is(err, ErrCollectorActive) {
		t.Fatalf("second acquire err=%v", err)
	}
	if err := store.MarkCollectorReady("instance-a"); err != nil {
		t.Fatal(err)
	}
	requested, err := store.RequestCollectorStop()
	if err != nil || !requested {
		t.Fatalf("request stop=%t err=%v", requested, err)
	}
	stop, err := store.HeartbeatCollector("instance-a")
	if err != nil || !stop {
		t.Fatalf("heartbeat stop=%t err=%v", stop, err)
	}
	if err := store.ReleaseCollectorLease("instance-a", "stopped", ""); err != nil {
		t.Fatal(err)
	}
	status, err := store.CollectorRuntimeStatus(time.Minute)
	if err != nil || status.State != "stopped" || status.InstanceID != "instance-a" || status.LastError != "" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	requested, err = store.RequestCollectorStop()
	if err != nil || requested {
		t.Fatalf("repeat stop=%t err=%v", requested, err)
	}
}

func TestCollectorLeaseRecoversOnlyAfterStaleHeartbeat(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.AcquireCollectorLease("old", 1, "v1", "background", time.Second, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE collector_runtime SET heartbeat_at = ? WHERE singleton = 1`, formatStoredTime(time.Now().Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.AcquireCollectorLease("new", 2, "v2", "background", time.Second, time.Minute)
	if err != nil || !recovered {
		t.Fatalf("stale takeover recovered=%t err=%v", recovered, err)
	}
	if stop, err := store.HeartbeatCollector("old"); !errors.Is(err, ErrCollectorLeaseLost) || stop {
		t.Fatalf("old heartbeat stop=%t err=%v", stop, err)
	}
	status, err := store.CollectorRuntimeStatus(time.Minute)
	if err != nil || !status.RecoveredFromStale {
		t.Fatalf("recovery status=%+v err=%v", status, err)
	}
	var terminalState string
	if err := store.db.QueryRow(`SELECT terminal_state FROM collector_runs WHERE instance_id = 'old'`).Scan(&terminalState); err != nil || terminalState != "stale" {
		t.Fatalf("old run terminal state=%q err=%v", terminalState, err)
	}
}

func TestMigrationElevenCreatesCollectorPersistence(t *testing.T) {
	store := openTestStore(t)
	for _, table := range []string{"collector_sources", "collector_runtime", "collector_runs"} {
		var name string
		if err := store.db.QueryRow(`SELECT name FROM sqlite_schema WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil || name != table {
			t.Fatalf("table %s name=%q err=%v", table, name, err)
		}
	}
	status, err := store.CollectorRuntimeStatus(0)
	if err != nil || status.State != "stopped" || status.FormatVersion != 1 {
		t.Fatalf("initial status=%+v err=%v", status, err)
	}
}

func TestCollectorPersistedHealthIsSanitized(t *testing.T) {
	store := openTestStore(t)
	root := t.TempDir()
	registered, err := store.RegisterCollectorSource(root, true)
	if err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(root, "private.jsonl")
	message := "read " + private + " token=private-secret"
	if err := store.UpdateCollectorSourceHealth(registered.Source.ID, "missing", message); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireCollectorLease("sanitized", 1, "v1", "background", time.Second, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteCollectorPoll("sanitized", ImportResult{}, CollectorHealth{}, errors.New(message)); err != nil {
		t.Fatal(err)
	}
	status, err := store.CollectorRuntimeStatus(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := store.ListCollectorSources(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{status.LastError, sources[0].LastError} {
		if value == "" || strings.Contains(value, private) || strings.Contains(value, "private-secret") {
			t.Fatalf("persisted health was not safely sanitized: %q", value)
		}
	}
}
