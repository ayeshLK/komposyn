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

package tview

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	modelprovider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/ui"
	"github.com/gdamore/tcell/v2"
	tv "github.com/rivo/tview"
)

func TestClipboardQueueSerializesAndKeepsNewestPendingWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan string, 2)
	release := make(chan struct{})
	finished := make(chan []string, 1)
	var writes []string
	q := newClipboardQueue(ctx, clipboardFunc(func(_ context.Context, text string) error {
		started <- text
		if text == "one" {
			<-release
		}
		writes = append(writes, text)
		if text == "three" {
			finished <- append([]string(nil), writes...)
		}
		return nil
	}), nil, nil)
	if !q.enqueue("one") {
		t.Fatal("first enqueue failed")
	}
	if got := <-started; got != "one" {
		t.Fatalf("started %q", got)
	}
	if !q.enqueue("two") || !q.enqueue("three") {
		t.Fatal("pending enqueue failed")
	}
	close(release)
	select {
	case got := <-finished:
		if !reflect.DeepEqual(got, []string{"one", "three"}) {
			t.Fatalf("write order %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("clipboard writes did not finish")
	}
	if q.enqueue("") {
		t.Fatal("empty copy was queued")
	}
}

func TestClipboardQueueTimeoutFallsBackAndReports(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fallback := make(chan string, 1)
	result := make(chan string, 1)
	q := newClipboardQueue(ctx, clipboardFunc(func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}), func(text string) { fallback <- text }, func(message string) { result <- message })
	if !q.enqueue("payload") {
		t.Fatal("enqueue failed")
	}
	select {
	case got := <-fallback:
		if got != "payload" {
			t.Fatalf("fallback text %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("clipboard timeout did not reach fallback")
	}
	select {
	case got := <-result:
		if got == "" {
			t.Fatal("missing status")
		}
	case <-time.After(time.Second):
		t.Fatal("clipboard result was not reported")
	}
}

func TestCopyTranscriptKeepsLiteralSourceAndExcludesNotices(t *testing.T) {
	u := &terminal{output: newTranscriptView()}
	u.write("banner notice")
	u.writeUser("  question\n\tline  ")
	u.writeAssistant("literal [red] text\n  code\t😀")
	u.write("provider status")
	want := "  question\n\tline  \n\nliteral [red] text\n  code\t😀"
	if got := u.copyableTranscript(); got != want {
		t.Fatalf("copyable transcript=%q, want %q", got, want)
	}
}

func TestMultilinePasteRemainsLiteralDraftAndDoesNotRunSlashCommands(t *testing.T) {
	u := &terminal{input: newComposerInput(), output: newTranscriptView(), client: &clientStub{}, commands: supportedCommands(), commandList: tv.NewList(), composer: tv.NewFlex()}
	pasted := "/copy all\n\tsecond line \\x1b[31m\n"
	u.input.PasteHandler()(pasted, func(tv.Primitive) {})
	if got := u.input.GetText(); got != pasted {
		t.Fatalf("pasted draft=%q, want %q", got, pasted)
	}
	if u.suggestionsActive() {
		t.Fatal("multiline slash paste opened command suggestions")
	}
	u.submit()
	if got := u.input.GetText(); got != pasted {
		t.Fatalf("draft was submitted or discarded without a model: %q", got)
	}
	if len(u.transcript) != 1 || !strings.Contains(u.transcript[0].text, "Choose a model") {
		t.Fatalf("pasted slash line was treated as a command: %#v", u.transcript)
	}
}

func TestClipboardQueueReportsNativeFailureWithoutFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan string, 1)
	q := newClipboardQueue(ctx, clipboardFunc(func(context.Context, string) error { return errors.New("failure") }), nil, func(message string) { result <- message })
	if !q.enqueue("text") {
		t.Fatal("enqueue failed")
	}
	select {
	case got := <-result:
		if got == "" {
			t.Fatal("missing failure status")
		}
	case <-time.After(time.Second):
		t.Fatal("failure status was not reported")
	}
}

func TestClipboardFailureFallbackDoesNotRaceScreenRedraw(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	safe := &synchronizedSimulation{SimulationScreen: screen}
	client := &clientStub{selected: &ui.Model{Key: "m", ID: "fixture"}}
	client.sendHook = func(_ context.Context, _ string, delta func(string)) (ui.Turn, error) {
		delta("copy me")
		return ui.Turn{Text: "copy me", Outcome: modelprovider.OutcomeCompleted}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (Plugin{screen: safe, clipboard: clipboardFunc(func(context.Context, string) error {
			return errors.New("native helper failed")
		})}).Run(ctx, client)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("terminal did not stop")
		}
	}()
	waitForScreen(t, safe, "KompoSyn", "")
	for _, r := range "question" {
		screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitForScreen(t, safe, "copy me", "")
	for _, r := range "/copy" {
		screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitForScreen(t, safe, "Clipboard request sent to the terminal", "")
	if got := string(screen.GetClipboardData()); got != "copy me" {
		t.Fatalf("OSC 52 fallback copied %q", got)
	}
}
