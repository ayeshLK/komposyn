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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tokenlogger "github.com/ayeshLK/komposyn/plugins/library/token-logger"
)

const watchUsage = "usage: komposyn-lg watch {add|remove|list|start|status|stop|foreground} ..."

var watchSubcommands = map[string]bool{
	"add": true, "remove": true, "list": true, "start": true,
	"status": true, "stop": true, "foreground": true, "serve": true,
}

func runWatchWithContext(args []string, stdout, stderr io.Writer, ctx context.Context) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, watchUsage)
		return 1
	}
	if !watchSubcommands[args[0]] {
		return runForegroundWatch(args, stdout, stderr, ctx, false)
	}
	switch args[0] {
	case "add":
		return runWatchAdd(args[1:], stdout, stderr)
	case "remove":
		return runWatchRemove(args[1:], stdout, stderr)
	case "list":
		return runWatchList(args[1:], stdout, stderr)
	case "start":
		return runWatchStart(args[1:], stdout, stderr)
	case "status":
		return runWatchStatus(args[1:], stdout, stderr)
	case "stop":
		return runWatchStop(args[1:], stdout, stderr)
	case "foreground":
		return runForegroundWatch(args[1:], stdout, stderr, ctx, true)
	case "serve":
		return runWatchServe(args[1:], stderr, ctx)
	default:
		fmt.Fprintln(stderr, watchUsage)
		return 1
	}
}

type watchOptions struct {
	dataDir     string
	format      string
	formatSet   bool
	interval    time.Duration
	intervalSet bool
	timeout     time.Duration
	timeoutSet  bool
	defaults    bool
	instanceID  string
}

func parseWatchOptions(args []string, positional bool) ([]string, watchOptions, error) {
	options := watchOptions{format: "text", timeout: 10 * time.Second}
	values := []string{}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if !strings.HasPrefix(argument, "--") {
			if !positional {
				return nil, options, fmt.Errorf("unexpected argument %q", argument)
			}
			values = append(values, argument)
			continue
		}
		switch argument {
		case "--defaults":
			options.defaults = true
		case "--data-dir", "--format", "--interval", "--timeout", "--instance":
			if index+1 >= len(args) {
				return nil, options, fmt.Errorf("missing value for %s", argument)
			}
			index++
			value := args[index]
			switch argument {
			case "--data-dir":
				options.dataDir = value
			case "--format":
				if value != "text" && value != "json" {
					return nil, options, errors.New("--format expects text or json")
				}
				options.format = value
				options.formatSet = true
			case "--interval":
				parsed, err := time.ParseDuration(value)
				if err != nil || parsed <= 0 {
					return nil, options, errors.New("interval must be a positive duration")
				}
				options.interval, options.intervalSet = parsed, true
			case "--timeout":
				parsed, err := time.ParseDuration(value)
				if err != nil || parsed <= 0 {
					return nil, options, errors.New("timeout must be a positive duration")
				}
				options.timeout = parsed
				options.timeoutSet = true
			case "--instance":
				options.instanceID = value
			}
		default:
			return nil, options, fmt.Errorf("unknown watch option %q", argument)
		}
	}
	return values, options, nil
}

func validateWatchOptions(options watchOptions, allowed ...string) error {
	permitted := map[string]bool{"data-dir": true}
	for _, option := range allowed {
		permitted[option] = true
	}
	for _, used := range []struct {
		name  string
		value bool
	}{
		{"format", options.formatSet}, {"interval", options.intervalSet}, {"timeout", options.timeoutSet},
		{"defaults", options.defaults}, {"instance", options.instanceID != ""},
	} {
		if used.value && !permitted[used.name] {
			return fmt.Errorf("--%s is not valid for this watch command", used.name)
		}
	}
	return nil
}

func openWatchStore(dataDir string, stderr io.Writer) (*tokenlogger.Store, string, error) {
	resolution, err := resolveDataDir(dataDir)
	if err != nil {
		return nil, "", fmt.Errorf("resolve token logger data directory: %w", err)
	}
	reportDataDirResolution(stderr, resolution)
	store, err := tokenlogger.Open(filepath.Join(resolution.Path, "token-logger.db"))
	if err != nil {
		return nil, "", fmt.Errorf("open token logger database in %s: %w", resolution.Path, err)
	}
	return store, resolution.Path, nil
}

func runWatchAdd(args []string, stdout, stderr io.Writer) int {
	paths, options, err := parseWatchOptions(args, true)
	if err == nil {
		err = validateWatchOptions(options, "defaults")
	}
	if err != nil || (len(paths) == 0 && !options.defaults) {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		fmt.Fprintln(stderr, "usage: komposyn-lg watch add <source> [<source>...] [--defaults] [--data-dir <path>]")
		return 1
	}
	if options.defaults {
		for _, root := range platformDefaultCollectorRoots() {
			paths = append(paths, root.Path)
		}
	}
	store, _, err := openWatchStore(options.dataDir, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer store.Close()
	failed := false
	invalid := false
	for _, path := range paths {
		result, addErr := store.RegisterCollectorSource(path, !options.defaults || !isDefaultWatchPath(path))
		if addErr != nil {
			fmt.Fprintf(stderr, "add source %q: %v\n", path, addErr)
			failed = true
			invalid = invalid || errors.Is(addErr, tokenlogger.ErrInvalidCollectorSource)
			continue
		}
		state := "unchanged"
		if result.Changed {
			state = "registered"
		}
		fmt.Fprintf(stdout, "%s source id=%s path=%s\n", state, result.Source.ID, tokenlogger.FormatHumanText(result.Source.Path))
	}
	if failed && invalid {
		return 1
	}
	if failed {
		return 2
	}
	return 0
}

func isDefaultWatchPath(path string) bool {
	for _, root := range platformDefaultCollectorRoots() {
		left, _ := filepath.Abs(root.Path)
		right, _ := filepath.Abs(path)
		if filepath.Clean(left) == filepath.Clean(right) {
			return true
		}
	}
	return false
}

func runWatchRemove(args []string, stdout, stderr io.Writer) int {
	selectors, options, err := parseWatchOptions(args, true)
	if err == nil {
		err = validateWatchOptions(options)
	}
	if err != nil || len(selectors) == 0 {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		fmt.Fprintln(stderr, "usage: komposyn-lg watch remove <source-or-id> [<source-or-id>...] [--data-dir <path>]")
		return 1
	}
	store, _, err := openWatchStore(options.dataDir, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer store.Close()
	for _, selector := range selectors {
		source, changed, removeErr := store.RemoveCollectorSource(selector)
		if removeErr != nil {
			fmt.Fprintf(stderr, "remove source %q: %v\n", selector, removeErr)
			return 2
		}
		if source.ID == "" {
			fmt.Fprintf(stdout, "source not registered: %s\n", selector)
			continue
		}
		state := "already disabled"
		if changed {
			state = "disabled"
		}
		fmt.Fprintf(stdout, "%s source id=%s path=%s\n", state, source.ID, tokenlogger.FormatHumanText(source.Path))
	}
	return 0
}

type sourceListOutput struct {
	FormatVersion int                            `json:"format_version"`
	Sources       []tokenlogger.RegisteredSource `json:"sources"`
}

func runWatchList(args []string, stdout, stderr io.Writer) int {
	_, options, err := parseWatchOptions(args, false)
	if err == nil {
		err = validateWatchOptions(options, "format")
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store, _, err := openWatchStore(options.dataDir, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer store.Close()
	sources, err := store.ListCollectorSources(false)
	if err != nil {
		fmt.Fprintf(stderr, "list sources: %v\n", err)
		return 2
	}
	if options.format == "json" {
		if err := json.NewEncoder(stdout).Encode(sourceListOutput{FormatVersion: 1, Sources: sources}); err != nil {
			fmt.Fprintf(stderr, "write source list: %v\n", err)
			return 2
		}
		return 0
	}
	if len(sources) == 0 {
		fmt.Fprintln(stdout, "no registered sources")
		return 0
	}
	for _, source := range sources {
		fmt.Fprintf(stdout, "id=%s enabled=%t status=%s path=%s", source.ID, source.Enabled, source.Status, tokenlogger.FormatHumanText(source.Path))
		if source.LastError != "" {
			fmt.Fprintf(stdout, " error=%s", source.LastError)
		}
		fmt.Fprintln(stdout)
	}
	return 0
}

func configuredWatchInterval(dataDir string) time.Duration {
	settings, err := tokenlogger.LoadSettings(filepath.Join(dataDir, "settings.json"), dataDir)
	if err != nil {
		return 5 * time.Second
	}
	return settings.CollectorPollInterval()
}

func persistWatchInterval(dataDir string, interval time.Duration) error {
	path := filepath.Join(dataDir, "settings.json")
	settings, err := tokenlogger.LoadSettings(path, dataDir)
	if err != nil {
		return err
	}
	settings.PollIntervalMillis = interval.Milliseconds()
	settings.PollIntervalSeconds = int((interval + time.Second - 1) / time.Second)
	return tokenlogger.SaveSettings(path, settings)
}

func staleWindow(interval time.Duration) time.Duration {
	window := 3 * interval
	if window < 15*time.Second {
		return 15 * time.Second
	}
	return window
}

func runWatchStart(args []string, stdout, stderr io.Writer) int {
	_, options, err := parseWatchOptions(args, false)
	if err == nil {
		err = validateWatchOptions(options, "interval")
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store, dataDir, err := openWatchStore(options.dataDir, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	interval := configuredWatchInterval(dataDir)
	if options.intervalSet {
		interval = options.interval
	}
	sources, err := store.ListCollectorSources(true)
	if err != nil {
		store.Close()
		fmt.Fprintf(stderr, "list enabled sources: %v\n", err)
		return 2
	}
	if len(sources) == 0 {
		store.Close()
		fmt.Fprintln(stderr, "watch start requires at least one enabled registered source")
		return 1
	}
	status, err := store.CollectorRuntimeStatus(staleWindow(interval))
	if err != nil {
		store.Close()
		fmt.Fprintf(stderr, "read collector status: %v\n", err)
		return 2
	}
	if options.intervalSet {
		if err := persistWatchInterval(dataDir, interval); err != nil {
			store.Close()
			fmt.Fprintf(stderr, "save collector interval: %v\n", err)
			return 2
		}
	}
	if status.State == "starting" || status.State == "running" || status.State == "degraded" || status.State == "stopping" {
		store.Close()
		fmt.Fprintf(stdout, "collector already %s instance=%s pid=%d\n", status.State, status.InstanceID, status.PID)
		return 0
	}
	instanceID, err := tokenlogger.NewCollectorInstanceID()
	if err != nil {
		store.Close()
		fmt.Fprintln(stderr, err)
		return 2
	}
	executable, err := os.Executable()
	if err != nil {
		store.Close()
		fmt.Fprintf(stderr, "resolve executable: %v\n", err)
		return 2
	}
	store.Close()
	childArgs := []string{"watch", "serve", "--data-dir", dataDir, "--interval", interval.String(), "--instance", instanceID}
	if err := startDetachedCollector(executable, childArgs); err != nil {
		fmt.Fprintf(stderr, "start background collector: %v\n", err)
		return 2
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		check, _, openErr := openWatchStore(dataDir, io.Discard)
		if openErr != nil {
			continue
		}
		current, statusErr := check.CollectorRuntimeStatus(staleWindow(interval))
		check.Close()
		if statusErr != nil {
			continue
		}
		if (current.State == "running" || current.State == "degraded") && (current.InstanceID == instanceID || current.InstanceID != "") {
			fmt.Fprintf(stdout, "collector started state=%s instance=%s pid=%d interval=%s recovered_stale=%t\n", current.State, current.InstanceID, current.PID, current.Interval, current.RecoveredFromStale)
			return 0
		}
	}
	fmt.Fprintln(stderr, "collector did not publish a ready heartbeat within 10s")
	return 2
}

func runWatchStatus(args []string, stdout, stderr io.Writer) int {
	_, options, err := parseWatchOptions(args, false)
	if err == nil {
		err = validateWatchOptions(options, "format")
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store, _, err := openWatchStore(options.dataDir, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer store.Close()
	status, err := store.CollectorRuntimeStatus(0)
	if err != nil {
		fmt.Fprintf(stderr, "read collector status: %v\n", err)
		return 2
	}
	status, err = store.CollectorRuntimeStatus(staleWindow(status.Interval))
	if err != nil {
		fmt.Fprintf(stderr, "read collector status: %v\n", err)
		return 2
	}
	if options.format == "json" {
		if err := json.NewEncoder(stdout).Encode(status); err != nil {
			fmt.Fprintf(stderr, "write collector status: %v\n", err)
			return 2
		}
		return 0
	}
	fmt.Fprintf(stdout, "collector state=%s mode=%s instance=%s pid=%d version=%s interval=%s roots=%d sources=%d imported=%d duplicates=%d pending=%t recovered_stale=%t",
		status.State, status.Mode, status.InstanceID, status.PID, status.ExecutableVersion, status.Interval, status.SourceCount, status.DiscoveredSources, status.Imported, status.Duplicates, status.Pending, status.RecoveredFromStale)
	if status.HeartbeatAt != nil {
		fmt.Fprintf(stdout, " heartbeat=%s", status.HeartbeatAt.Format(time.RFC3339Nano))
	}
	if status.LastCompletedAt != nil {
		fmt.Fprintf(stdout, " last_completed=%s", status.LastCompletedAt.Format(time.RFC3339Nano))
	}
	if status.LastError != "" {
		fmt.Fprintf(stdout, " error=%s", status.LastError)
	}
	fmt.Fprintln(stdout)
	return 0
}

func runWatchStop(args []string, stdout, stderr io.Writer) int {
	_, options, err := parseWatchOptions(args, false)
	if err == nil {
		err = validateWatchOptions(options, "timeout")
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store, _, err := openWatchStore(options.dataDir, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	status, err := store.CollectorRuntimeStatus(0)
	if err != nil {
		store.Close()
		fmt.Fprintf(stderr, "read collector status: %v\n", err)
		return 2
	}
	status, err = store.CollectorRuntimeStatus(staleWindow(status.Interval))
	if err != nil {
		store.Close()
		fmt.Fprintf(stderr, "read collector status: %v\n", err)
		return 2
	}
	if status.State == "stale" {
		store.Close()
		fmt.Fprintf(stdout, "collector is stale; no process was signalled (instance=%s pid=%d)\n", status.InstanceID, status.PID)
		return 0
	}
	requested, err := store.RequestCollectorStop()
	if err != nil {
		store.Close()
		fmt.Fprintf(stderr, "request collector stop: %v\n", err)
		return 2
	}
	if !requested {
		store.Close()
		fmt.Fprintln(stdout, "collector already stopped")
		return 0
	}
	deadline := time.Now().Add(options.timeout)
	for time.Now().Before(deadline) {
		status, statusErr := store.CollectorRuntimeStatus(0)
		if statusErr != nil {
			store.Close()
			fmt.Fprintf(stderr, "read collector status: %v\n", statusErr)
			return 2
		}
		if status.State == "stopped" || status.State == "failed" {
			store.Close()
			fmt.Fprintf(stdout, "collector stopped instance=%s\n", status.InstanceID)
			return 0
		}
		time.Sleep(50 * time.Millisecond)
	}
	store.Close()
	fmt.Fprintln(stderr, "collector did not stop before timeout; retry 'watch stop' after the current atomic import completes")
	return 3
}

func runForegroundWatch(args []string, stdout, stderr io.Writer, ctx context.Context, registryFallback bool) int {
	roots, options, err := parseWatchOptions(args, true)
	if err == nil {
		err = validateWatchOptions(options, "interval")
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store, dataDir, err := openWatchStore(options.dataDir, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer store.Close()
	if len(roots) == 0 && registryFallback {
		sources, listErr := store.ListCollectorSources(true)
		if listErr != nil {
			fmt.Fprintf(stderr, "list enabled sources: %v\n", listErr)
			return 2
		}
		for _, source := range sources {
			roots = append(roots, source.Path)
		}
	}
	if len(roots) == 0 {
		fmt.Fprintln(stderr, "watch foreground requires a source or an enabled registered source")
		return 1
	}
	interval := configuredWatchInterval(dataDir)
	if options.intervalSet {
		interval = options.interval
	}
	collector, err := tokenlogger.NewCollector(store, roots, interval)
	if err != nil {
		fmt.Fprintf(stderr, "create collector: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "watching %d source root(s) every %s; press Ctrl-C to stop\n", len(roots), interval)
	result, pollErr := collector.PollOnceContext(ctx)
	writeWatchPoll(stdout, result, collector.Health())
	if pollErr != nil {
		fmt.Fprintf(stderr, "initial collector poll: %v\n", pollErr)
	}
	if err := collector.RunWithResults(ctx, func(result tokenlogger.ImportResult, pollErr error) {
		writeWatchPoll(stdout, result, collector.Health())
		if pollErr != nil {
			fmt.Fprintf(stderr, "collector poll: %v\n", pollErr)
		}
	}); err != nil {
		fmt.Fprintf(stderr, "collector stopped: %v\n", err)
		return 2
	}
	writeWatchSummary(stdout, collector.Health())
	return 0
}

func runWatchServe(args []string, stderr io.Writer, ctx context.Context) int {
	_, options, err := parseWatchOptions(args, false)
	if err == nil {
		err = validateWatchOptions(options, "interval", "instance")
	}
	if err != nil || options.instanceID == "" || !options.intervalSet {
		fmt.Fprintln(stderr, "invalid internal watch serve invocation")
		return 1
	}
	store, _, err := openWatchStore(options.dataDir, io.Discard)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer store.Close()
	recovered, err := store.AcquireCollectorLease(options.instanceID, os.Getpid(), tokenlogger.Version, "background", options.interval, staleWindow(options.interval))
	if errors.Is(err, tokenlogger.ErrCollectorActive) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "acquire collector lease: %v\n", err)
		return 2
	}
	terminalState, terminalError := "stopped", ""
	defer func() { _ = store.ReleaseCollectorLease(options.instanceID, terminalState, terminalError) }()
	if err := store.MarkCollectorReady(options.instanceID); err != nil {
		terminalState, terminalError = "failed", err.Error()
		return 2
	}
	if recovered {
		_, _ = store.RecordDiagnostic("reason=stale_collector_recovered")
	}
	for {
		stop, heartbeatErr := store.HeartbeatCollector(options.instanceID)
		if heartbeatErr != nil {
			terminalState, terminalError = "failed", heartbeatErr.Error()
			return 2
		}
		if stop || ctx.Err() != nil {
			return 0
		}
		sources, listErr := store.ListCollectorSources(true)
		if listErr != nil {
			terminalState, terminalError = "failed", listErr.Error()
			return 2
		}
		roots := make([]string, 0, len(sources))
		for _, source := range sources {
			roots = append(roots, source.Path)
			status, message := "healthy", ""
			if _, statErr := os.Stat(source.Path); statErr != nil {
				status, message = "missing", statErr.Error()
			}
			_ = store.UpdateCollectorSourceHealth(source.ID, status, message)
		}
		if err := store.BeginCollectorPoll(options.instanceID, len(sources)); err != nil {
			terminalState, terminalError = "failed", err.Error()
			return 2
		}
		collector, err := tokenlogger.NewCollector(store, roots, options.interval)
		if err != nil {
			terminalState, terminalError = "failed", err.Error()
			return 2
		}
		result, pollErr, heartbeatErr := pollWithLeaseHeartbeat(store, options.instanceID, collector, staleWindow(options.interval))
		if heartbeatErr != nil {
			terminalState, terminalError = "failed", heartbeatErr.Error()
			return 2
		}
		if err := store.CompleteCollectorPoll(options.instanceID, result, collector.Health(), pollErr); err != nil {
			terminalState, terminalError = "failed", err.Error()
			return 2
		}
		waitUntil := time.Now().Add(options.interval)
		for time.Now().Before(waitUntil) {
			remaining := time.Until(waitUntil)
			delay := time.Second
			if remaining < delay {
				delay = remaining
			}
			select {
			case <-ctx.Done():
				return 0
			case <-time.After(delay):
			}
			stop, heartbeatErr = store.HeartbeatCollector(options.instanceID)
			if heartbeatErr != nil {
				terminalState, terminalError = "failed", heartbeatErr.Error()
				return 2
			}
			if stop {
				return 0
			}
		}
	}
}

func pollWithLeaseHeartbeat(store *tokenlogger.Store, instanceID string, collector *tokenlogger.Collector, staleAfter time.Duration) (tokenlogger.ImportResult, error, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interval := leaseHeartbeatInterval(staleAfter)
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- nil
				return
			case <-ticker.C:
				_, err := store.HeartbeatCollector(instanceID)
				if err != nil {
					cancel()
					done <- err
					return
				}
			}
		}
	}()
	result, pollErr := collector.PollOnceContext(ctx)
	cancel()
	heartbeatErr := <-done
	return result, pollErr, heartbeatErr
}

func leaseHeartbeatInterval(staleAfter time.Duration) time.Duration {
	interval := staleAfter / 3
	if interval > 5*time.Second {
		interval = 5 * time.Second
	}
	if interval < time.Second {
		interval = time.Second
	}
	return interval
}
