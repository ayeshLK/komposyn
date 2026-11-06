// Copyright 2026 Ayesh Almeida
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build linux

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ayeshLK/komposyn/internal/research/sqlitevfsstudy"
)

func main() {
	commit := flag.String("commit", "", "exact repository source commit for this run (required)")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), sqlitevfsstudy.MaxRunTime)
	defer cancel()
	if err := verifySource(ctx, *commit); err != nil {
		fmt.Fprintf(os.Stderr, "source verification failed: %v\n", err)
		os.Exit(1)
	}
	report, err := sqlitevfsstudy.Run(ctx, *commit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sqlite VFS study failed: %v\n", err)
		os.Exit(1)
	}
	encoded, err := report.Marshal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(append(encoded, '\n')); err != nil {
		fmt.Fprintf(os.Stderr, "write report: %v\n", err)
		os.Exit(1)
	}
}

func verifySource(ctx context.Context, expected string) error {
	if expected == "" {
		return fmt.Errorf("--commit is required")
	}
	output, err := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD^{commit}").Output()
	if err != nil {
		return fmt.Errorf("read HEAD commit from current repository: %w", err)
	}
	if actual := strings.TrimSpace(string(output)); actual != expected {
		return fmt.Errorf("--commit does not match current HEAD")
	}
	status, err := exec.CommandContext(ctx, "git", "status", "--porcelain=v1", "--untracked-files=normal").Output()
	if err != nil {
		return fmt.Errorf("inspect source worktree: %w", err)
	}
	if len(status) != 0 {
		return fmt.Errorf("source worktree is not clean")
	}
	return nil
}
