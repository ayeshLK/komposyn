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

//go:build linux

// Command sandbox-study runs disposable issue #215 experiments. It is not
// shipped or connected to the KompoSyn runtime.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/ayeshLK/komposyn/internal/harness/sandboxstudy"
	"golang.org/x/sys/unix"
)

type report struct {
	Study         string                     `json:"study"`
	Host          host                       `json:"host"`
	Policy        []string                   `json:"effective_candidate_policy"`
	TypedWorker   string                     `json:"typed_worker"`
	Shell         string                     `json:"shell"`
	Observations  []sandboxstudy.Observation `json:"observations,omitempty"`
	Unavailable   string                     `json:"unavailable,omitempty"`
	ResourceNotes []string                   `json:"resource_controls"`
}

type host struct {
	RepositoryCommit           string `json:"repository_commit"`
	OS                         string `json:"os"`
	OSRelease                  string `json:"os_release,omitempty"`
	GoVersion                  string `json:"go_version"`
	Architecture               string `json:"architecture"`
	Kernel                     string `json:"kernel"`
	BubblewrapPath             string `json:"bubblewrap_path"`
	BubblewrapVersion          string `json:"bubblewrap_version,omitempty"`
	BubblewrapSHA256           string `json:"bubblewrap_sha256,omitempty"`
	BubblewrapOwner            string `json:"bubblewrap_owner,omitempty"`
	BubblewrapMode             string `json:"bubblewrap_mode,omitempty"`
	UserNamespaceMax           string `json:"user_namespace_max,omitempty"`
	UIDMap                     string `json:"uid_map,omitempty"`
	CgroupPath                 string `json:"cgroup_path,omitempty"`
	HostCgroupControllers      string `json:"host_cgroup_controllers,omitempty"`
	CgroupControllers          string `json:"cgroup_controllers,omitempty"`
	CgroupSubtreeControl       string `json:"cgroup_subtree_control,omitempty"`
	CgroupWritable             bool   `json:"current_cgroup_control_file_writable"`
	CgroupParentSubtreeControl string `json:"cgroup_parent_subtree_control,omitempty"`
	CgroupParentWritable       bool   `json:"cgroup_parent_control_file_writable"`
	NoNewPrivs                 string `json:"no_new_privs,omitempty"`
	Seccomp                    string `json:"seccomp_mode,omitempty"`
	EffectiveCaps              string `json:"effective_capabilities,omitempty"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--sandbox-study-worker" {
		os.Exit(sandboxstudy.WorkerMain(os.Args[2:], os.Stdin, os.Stdout))
	}
	backend := flag.String("bwrap", "/usr/bin/bwrap", "trusted absolute path to the system bubblewrap executable")
	commit := flag.String("commit", "unknown", "repository commit under study")
	flag.Parse()
	if !filepath.IsAbs(*backend) {
		fmt.Fprintln(os.Stderr, "--bwrap must be an absolute path")
		os.Exit(2)
	}
	report := report{
		Study: "issue-215 disposable Linux feasibility study",
		Host:  hostSnapshot(*backend, *commit),
		Policy: []string{
			"unshare user, PID, IPC, UTS, and network namespaces (all required; no try/fallback flags)",
			"die-with-parent; new session; disable nested user namespaces; drop all capabilities",
			"empty tmpfs root; read-only bind of this statically linked worker; only fixture root is writable",
			"fixture protected subpath may be hidden with a private tmpfs mask; alias behavior is tested separately",
			"clear environment; set HOME=/tmp/home TMPDIR=/tmp PATH=/nonexistent LANG=C.UTF-8",
			"no proc, sys, dev, host home, host root, host sockets, or inherited descriptors; stdio is the framed control channel",
			"architecture-checked seccomp filter denies socket/IPC, mount, namespace, ptrace, process inspection, and privilege-changing syscalls; remaining syscalls allowed",
			"private /tmp tmpfs is limited to 16 MiB; host enforces 5-second runtime and 64 KiB per output line",
			"no cgroup is configured; no exhaustion test is run; aggregate process/memory/CPU limits are unproven",
		},
		TypedWorker: "candidate typed Go worker only; no arbitrary arguments or shell parsing",
		Shell:       "not implemented or run; typed-worker evidence cannot establish shell safety",
		ResourceNotes: []string{
			"Bwrap namespace and mount setup is attempted by the native suite only; setup refusal is unavailable evidence, never a pass.",
			"No cgroup files are written; visible controllers and control-file write access do not prove a usable delegated subtree.",
			"Parent death, descendant escape, mount-crossing and aggregate cgroup resource tests are unavailable unless individually run by a native follow-up.",
		},
	}
	worker, err := os.Executable()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		report.Observations, err = sandboxstudy.RunNativeSuite(ctx, *backend, worker)
		cancel()
	}
	if err != nil {
		report.Unavailable = err.Error()
		if len(report.Observations) == 0 {
			report.Observations = []sandboxstudy.Observation{{Case: "candidate-startup", Attempt: "launch disposable typed worker with the exact bwrap profile", ExpectedA: "candidate must establish every required namespace and mount before worker startup", Status: "unavailable", Limitation: err.Error()}}
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err != nil {
		os.Exit(3)
	}
}

func hostSnapshot(backend, commit string) host {
	result := host{RepositoryCommit: commit, OS: runtime.GOOS, GoVersion: runtime.Version(), Architecture: runtime.GOARCH, BubblewrapPath: backend}
	result.OSRelease = osRelease()
	var uts unix.Utsname
	if err := unix.Uname(&uts); err == nil {
		result.Kernel = strings.TrimRight(string(uts.Release[:]), "\x00")
	}
	result.UserNamespaceMax = readTrimmed("/proc/sys/user/max_user_namespaces")
	result.UIDMap = readTrimmed("/proc/self/uid_map")
	status := readTrimmed("/proc/self/status")
	result.NoNewPrivs = statusLine(status, "NoNewPrivs:")
	result.Seccomp = statusLine(status, "Seccomp:")
	result.EffectiveCaps = statusLine(status, "CapEff:")
	result.CgroupPath = readTrimmed("/proc/self/cgroup")
	result.HostCgroupControllers = readTrimmed("/sys/fs/cgroup/cgroup.controllers")
	if path := currentCgroupPath(result.CgroupPath); path != "" {
		result.CgroupControllers = readTrimmed(filepath.Join(path, "cgroup.controllers"))
		result.CgroupSubtreeControl = readTrimmed(filepath.Join(path, "cgroup.subtree_control"))
		result.CgroupWritable = controlFileWritable(filepath.Join(path, "cgroup.procs"))
		parent := filepath.Dir(path)
		result.CgroupParentSubtreeControl = readTrimmed(filepath.Join(parent, "cgroup.subtree_control"))
		result.CgroupParentWritable = controlFileWritable(filepath.Join(parent, "cgroup.subtree_control"))
	}
	if sandboxstudy.ValidateBackend(backend) == nil {
		if info, err := os.Stat(backend); err == nil {
			result.BubblewrapMode = info.Mode().String()
			if stat, ok := info.Sys().(*syscall.Stat_t); ok {
				result.BubblewrapOwner = fmt.Sprintf("uid:%d gid:%d", stat.Uid, stat.Gid)
			}
		}
		if binary, err := os.ReadFile(backend); err == nil {
			digest := sha256.Sum256(binary)
			result.BubblewrapSHA256 = fmt.Sprintf("%x", digest)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, backend, "--version")
		command.Env = []string{}
		if output, err := command.Output(); err == nil {
			result.BubblewrapVersion = strings.TrimSpace(string(output))
		}
	}
	return result
}

func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "unavailable: " + err.Error()
	}
	return strings.TrimSpace(string(data))
}

func osRelease() string {
	for _, line := range strings.Split(readTrimmed("/etc/os-release"), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(line[len("PRETTY_NAME="):], "\"")
		}
	}
	return "unavailable"
}

func statusLine(status, prefix string) string {
	for _, line := range strings.Split(status, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return "unavailable"
}

func currentCgroupPath(cgroup string) string {
	for _, line := range strings.Split(cgroup, "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) == 3 && fields[0] == "0" && fields[1] == "" {
			return filepath.Join("/sys/fs/cgroup", filepath.Clean("/"+fields[2]))
		}
	}
	return ""
}

func controlFileWritable(path string) bool { return unix.Access(path, unix.W_OK) == nil }
