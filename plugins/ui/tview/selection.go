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
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	tv "github.com/rivo/tview"
	"github.com/rivo/uniseg"
)

func (v *transcriptView) setContent(text string, preserve, follow bool, offset int) {
	if preserve {
		v.TextArea.SetText(text, false)
		return
	}
	v.TextArea.SetText(text, follow)
	if follow {
		v.TextArea.SetOffset(max(0, visualRows(text, v.innerWidth())-v.innerHeight()), 0)
	} else {
		v.TextArea.SetOffset(offset, 0)
	}
}

func (v *transcriptView) innerWidth() int {
	_, _, width, _ := v.GetInnerRect()
	return max(1, width)
}

func (v *transcriptView) innerHeight() int {
	_, _, _, height := v.GetInnerRect()
	return max(1, height)
}

func (v *transcriptView) PasteHandler() func(string, func(tv.Primitive)) {
	return func(string, func(tv.Primitive)) {}
}

// MouseHandler keeps tview's internal TextArea from becoming the focused
// primitive. The wrapper owns paste suppression, so focus must remain here.
func (v *transcriptView) MouseHandler() func(tv.MouseAction, *tcell.EventMouse, func(tv.Primitive)) (bool, tv.Primitive) {
	handler := v.TextArea.MouseHandler()
	return func(action tv.MouseAction, event *tcell.EventMouse, setFocus func(tv.Primitive)) (bool, tv.Primitive) {
		consumed, capture := handler(action, event, func(primitive tv.Primitive) {
			if primitive == v.TextArea {
				setFocus(v)
				return
			}
			setFocus(primitive)
		})
		if capture == v.TextArea {
			capture = v
		}
		return consumed, capture
	}
}

func (v *transcriptView) InputHandler() func(*tcell.EventKey, func(tv.Primitive)) {
	handler := v.TextArea.InputHandler()
	return func(event *tcell.EventKey, setFocus func(tv.Primitive)) {
		handler(event, func(primitive tv.Primitive) {
			if primitive == v.TextArea {
				setFocus(v)
				return
			}
			setFocus(primitive)
		})
		if v.onNavigation != nil {
			switch event.Key() {
			case tcell.KeyUp, tcell.KeyDown, tcell.KeyHome, tcell.KeyEnd, tcell.KeyPgUp, tcell.KeyPgDn:
				v.onNavigation(event)
			}
		}
	}
}

func (u *terminal) updateTranscriptScrollAfterKeyboard(event *tcell.EventKey) {
	// TextArea has already moved its viewport while processing the key. Record
	// that actual offset instead of deriving another movement from the cursor.
	offset, _ := u.output.GetOffset()
	maxOffset := u.maxTranscriptScroll()
	u.transcriptScrollRow = max(0, offset)
	u.followingTranscript = u.transcriptScrollRow >= maxOffset
}

func selectByteRange(area *tv.TextArea, text string, start, end int, screen tcell.Screen) {
	start = max(0, min(len(text), start))
	end = max(start, min(len(text), end))
	area.SetText(text, false)
	area.Draw(screen)
	startClusters := uniseg.GraphemeClusterCount(text[:start])
	selectedClusters := uniseg.GraphemeClusterCount(text[start:end])
	handler := area.InputHandler()
	for range startClusters {
		handler(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone), func(tv.Primitive) {})
	}
	for range selectedClusters {
		handler(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModShift), func(tv.Primitive) {})
	}
}

func visualRows(text string, width int) int {
	width = max(1, width)
	rows := 0
	for _, line := range strings.Split(text, "\n") {
		columns := 0
		graphemes := uniseg.NewGraphemes(line)
		for graphemes.Next() {
			if graphemes.Str() == "\t" {
				columns += tv.TabSize
			} else {
				columns += uniseg.StringWidth(graphemes.Str())
			}
		}
		rows += max(1, (columns+width-1)/width)
	}
	return rows
}

func (u *terminal) transcriptRootActive() bool {
	return u.chatRootActive.Load()
}

func (u *terminal) transcriptFocused() bool {
	if u.app == nil || u.output == nil {
		return false
	}
	focus := u.app.GetFocus()
	return focus == u.output || focus == u.output.TextArea
}

func (u *terminal) captureSelectionSnapshot() {
	if !u.transcriptRootActive() {
		u.selectionNeedsRestore = false
		u.selectionSnapshot = false
		return
	}
	if !u.output.HasSelection() {
		return
	}
	_, u.selectionSnapshotFrom, u.selectionSnapshotTo = u.output.GetSelection()
	u.selectionSnapshot = true
	u.selectionNeedsRestore = true
}

func (u *terminal) restoreSelectionSnapshot(screen tcell.Screen) {
	if !u.selectionSnapshot || !u.selectionNeedsRestore || u.output.HasSelection() || !u.transcriptRootActive() {
		return
	}
	selectByteRange(u.output.TextArea, u.output.GetText(true), u.selectionSnapshotFrom, u.selectionSnapshotTo, screen)
	u.selectionNeedsRestore = false
	u.output.Draw(screen)
}

func (u *terminal) captureTranscriptMouse(event *tcell.EventMouse, action tv.MouseAction) (*tcell.EventMouse, tv.MouseAction) {
	if event == nil {
		return event, action
	}
	if !u.transcriptRootActive() {
		if action == tv.MouseLeftUp {
			u.selectionDragging.Store(false)
		}
		return event, action
	}
	x, y := event.Position()
	left, top, width, height := u.output.GetInnerRect()
	inside := x >= left && x < left+width && y >= top && y < top+height
	switch action {
	case tv.MouseLeftDown:
		if inside {
			u.selectionDragging.Store(true)
			u.selectionMoved.Store(false)
			u.selectionDownX, u.selectionDownY = x, y
			u.selectionMouseY.Store(int32(y))
			u.startSelectionAutoscroll()
		}
	case tv.MouseMove:
		if u.selectionDragging.Load() {
			if x != u.selectionDownX || y != u.selectionDownY {
				u.selectionMoved.Store(true)
				u.followingTranscript = false
			}
			u.selectionMouseY.Store(int32(y))
		}
	case tv.MouseLeftUp:
		if u.selectionDragging.Swap(false) {
			thirdClick := false
			if u.selectionMoved.Swap(false) {
				u.selectionClickCount = 0
			} else {
				thirdClick = u.trackTranscriptClick(x, y)
			}
			go func() {
				u.app.QueueUpdateDraw(func() {
					if thirdClick {
						u.selectLogicalLine()
					}
					if u.output.HasSelection() {
						u.followingTranscript = false
					}
					u.copySelection()
				})
			}()
		}
	case tv.MouseScrollUp:
		if inside {
			u.followingTranscript = false
			u.transcriptScrollRow = max(0, u.transcriptScrollRow-1)
		}
	case tv.MouseScrollDown:
		if inside {
			u.transcriptScrollRow = min(u.maxTranscriptScroll(), u.transcriptScrollRow+1)
			u.followingTranscript = u.transcriptScrollRow >= u.maxTranscriptScroll()
		}
	}
	return event, action
}

func (u *terminal) trackTranscriptClick(x, y int) bool {
	now := time.Now()
	if now.Sub(u.selectionClickTime) > 700*time.Millisecond || intAbs(x-u.selectionClickX) > 1 || intAbs(y-u.selectionClickY) > 1 {
		u.selectionClickCount = 1
	} else {
		u.selectionClickCount++
	}
	u.selectionClickTime = now
	u.selectionClickX, u.selectionClickY = x, y
	if u.selectionClickCount == 3 {
		u.selectionClickCount = 0
		return true
	}
	return false
}

func intAbs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func (u *terminal) selectLogicalLine() {
	text := u.output.GetText(true)
	_, cursor, _ := u.output.GetSelection()
	start := strings.LastIndex(text[:min(cursor, len(text))], "\n") + 1
	end := len(text)
	if newline := strings.Index(text[min(cursor, len(text)):], "\n"); newline >= 0 {
		end = min(cursor, len(text)) + newline
	}
	selectByteRange(u.output.TextArea, text, start, end, u.screen)
}

func (u *terminal) copySelection() {
	if !u.output.HasSelection() {
		if !u.selectionNeedsRestore {
			u.selectionSnapshot = false
		}
		return
	}
	text, _, _ := u.output.GetSelection()
	if text != "" && u.clipboard != nil && !u.clipboard.enqueue(text) {
		u.write("Clipboard queue is busy. Try copying again shortly.\n")
	}
}

func (u *terminal) startSelectionAutoscroll() {
	if u.ctx == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-u.ctx.Done():
				return
			case <-ticker.C:
				if !u.selectionDragging.Load() {
					return
				}
				y := int(u.selectionMouseY.Load())
				u.app.QueueUpdateDraw(func() {
					_, top, _, height := u.output.GetInnerRect()
					key := tcell.KeyRune
					if y < top {
						key = tcell.KeyUp
					} else if y >= top+height {
						key = tcell.KeyDown
					}
					if key != tcell.KeyRune {
						u.followingTranscript = false
						u.output.InputHandler()(tcell.NewEventKey(key, 0, tcell.ModShift), func(tv.Primitive) {})
					}
				})
			}
		}
	}()
}

func (u *terminal) maxTranscriptScroll() int {
	text := u.output.GetText(true)
	return max(0, visualRows(text, u.output.innerWidth())-u.output.innerHeight())
}

func (u *terminal) dismissSelection() {
	if !u.output.HasSelection() {
		return
	}
	u.output.InputHandler()(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone), func(tv.Primitive) {})
	u.selectionSnapshot = false
	u.selectionNeedsRestore = false
	u.followingTranscript = true
	u.transcriptScrollRow = u.maxTranscriptScroll()
	u.output.SetOffset(u.transcriptScrollRow, 0)
}

func selectTextAreaWord(area *tv.TextArea, x, y int) {
	handler := area.MouseHandler()
	setFocus := func(tv.Primitive) {}
	for _, action := range []tv.MouseAction{tv.MouseLeftDown, tv.MouseLeftUp, tv.MouseLeftDoubleClick} {
		handler(action, tcell.NewEventMouse(x, y, tcell.ButtonPrimary, tcell.ModNone), setFocus)
	}
}
