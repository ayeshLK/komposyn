package tview

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	modelprovider "github.com/ayeshLK/komposyn/plugins/model-provider"
	"github.com/ayeshLK/komposyn/plugins/ui"
	"github.com/gdamore/tcell/v2"
)

type clientStub struct {
	mu         sync.Mutex
	selected   *ui.Model
	selectedCh chan struct{}
}

// synchronizedSimulation protects the returned cell slice from tview's render
// goroutine while the test inspects a snapshot of the simulated screen.
type synchronizedSimulation struct {
	tcell.SimulationScreen
	mu sync.Mutex
}

func (s *synchronizedSimulation) Show() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SimulationScreen.Show()
}

func (s *synchronizedSimulation) GetContents() ([]tcell.SimCell, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cells, width, height := s.SimulationScreen.GetContents()
	snapshot := make([]tcell.SimCell, len(cells))
	copy(snapshot, cells)
	for i := range snapshot {
		snapshot[i].Runes = append([]rune(nil), cells[i].Runes...)
	}
	return snapshot, width, height
}

func (c *clientStub) Profiles(context.Context) ([]ui.Profile, error) { return nil, nil }
func (c *clientStub) Discover(context.Context) (ui.Discovery, error) {
	return ui.Discovery{Models: []ui.Model{{Key: "openai\x00personal\x00model-x", Provider: "openai", ProfileID: "personal", ProfileLabel: "Personal", ID: "model-x", DisplayName: "Text Model", Compatibility: modelprovider.CompatibilityUnknown}}}, nil
}
func (c *clientStub) AddProfile(context.Context, string, string, string, string, map[string]string) error {
	return nil
}
func (c *clientStub) ReplaceProfile(context.Context, string, string, string, map[string]string, bool) error {
	return nil
}
func (c *clientStub) RemoveProfile(string, bool) error { return nil }
func (c *clientStub) Select(_ context.Context, key string, _ bool) error {
	d, _ := c.Discover(context.Background())
	for _, m := range d.Models {
		if m.Key == key {
			c.mu.Lock()
			copy := m
			c.selected = &copy
			c.mu.Unlock()
			select {
			case c.selectedCh <- struct{}{}:
			default:
			}
			return nil
		}
	}
	return nil
}
func (c *clientStub) Snapshot() ui.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ui.Snapshot{Selected: c.selected}
}
func (c *clientStub) Send(context.Context, string, func(string)) (ui.Turn, error) {
	return ui.Turn{}, nil
}
func (c *clientStub) Cancel() {}

func TestPickerInteractionOnSimulationScreen(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(100, 32)
	safeScreen := &synchronizedSimulation{SimulationScreen: screen}
	client := &clientStub{selectedCh: make(chan struct{}, 1)}
	finished := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { finished <- (Plugin{screen: safeScreen}).Run(ctx, client) }()
	time.Sleep(150 * time.Millisecond)
	press := func(key tcell.Key, r rune) {
		screen.InjectKey(key, r, tcell.ModNone)
		time.Sleep(10 * time.Millisecond)
	}
	for _, r := range "/model" {
		press(tcell.KeyRune, r)
	}
	press(tcell.KeyEnter, 0)
	deadline := time.Now().Add(3 * time.Second)
	rendered := ""
	for time.Now().Before(deadline) {
		cells, _, _ := safeScreen.GetContents()
		var b strings.Builder
		for _, cell := range cells {
			b.WriteString(string(cell.Runes))
		}
		rendered = b.String()
		if strings.Contains(rendered, "Refresh Models") && strings.Contains(rendered, "Text Model") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(rendered, "Add New Provider") || !strings.Contains(rendered, "Refresh Models") || !strings.Contains(rendered, "Manage Providers") || !strings.Contains(rendered, "Text Model") {
		cancel()
		<-finished
		t.Fatalf("picker actions/models not rendered: %q", rendered)
	}
	// Enter leaves the filter field for the picker list; three Down presses move
	// from Add New Provider through the two persistent actions to the model.
	press(tcell.KeyEnter, 0)
	press(tcell.KeyDown, 0)
	press(tcell.KeyDown, 0)
	press(tcell.KeyDown, 0)
	press(tcell.KeyEnter, 0)
	select {
	case <-client.selectedCh:
	case <-time.After(2 * time.Second):
		cancel()
		<-finished
		t.Fatal("selecting the rendered model did not reach the client")
	}
	press(tcell.KeyCtrlC, 0)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("terminal did not stop")
	}
}
