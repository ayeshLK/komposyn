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

//go:build linux && amd64

// Command sqlite-write-barrier-study runs the non-production synthetic VFS
// experiment. It accepts no fixture paths or SQL from the caller.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ayeshLK/komposyn/internal/research/sqlitewritebarrier"
)

func main() {
	commit := flag.String("commit", "", "exact 40-character source commit")
	flag.Parse()
	if *commit == "" {
		fmt.Fprintln(os.Stderr, "--commit is required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	report, err := sqlitewritebarrier.Run(ctx, *commit)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if marshalErr := enc.Encode(report); marshalErr != nil {
		fmt.Fprintln(os.Stderr, "encode report:", marshalErr)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "study incomplete:", err)
		os.Exit(1)
	}
}
