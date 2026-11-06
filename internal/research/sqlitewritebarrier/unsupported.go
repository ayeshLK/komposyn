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

//go:build !linux || !amd64

package sqlitewritebarrier

import (
	"context"
	"errors"
)

type Decision uint8

const (
	Deny Decision = iota
	Allow
)

type Role string

const (
	MainDB      Role = "main_db"
	MainJournal Role = "main_journal"
)

type Registration struct {
	Sequence uint64
	Role     Role
	Name     string
	Flags    int32
	Device   uint64
	Inode    uint64
}
type Gate func(context.Context, Registration) (Decision, error)
type Event struct {
	Sequence uint64 `json:"sequence"`
	Kind     string `json:"kind"`
	Role     Role   `json:"role,omitempty"`
	Fixture  string `json:"fixture,omitempty"`
	Object   uint64 `json:"object,omitempty"`
	Matched  bool   `json:"identity_matched,omitempty"`
	Result   int32  `json:"sqlite_result"`
}
type Adapter struct{}

func New(context.Context, Gate) (*Adapter, error) {
	return nil, errors.New("writable SQLite object barrier is available only on reviewed linux/amd64 ABI")
}
func (*Adapter) Name() string         { return "" }
func (*Adapter) Events() []Event      { return nil }
func (*Adapter) EventsComplete() bool { return false }
func (*Adapter) Close(context.Context) error {
	return errors.New("writable SQLite object barrier is unavailable on this platform")
}

type StudyReport struct{}

func Run(context.Context, string) (StudyReport, error) {
	return StudyReport{}, errors.New("writable SQLite object barrier is available only on reviewed linux/amd64 ABI")
}
