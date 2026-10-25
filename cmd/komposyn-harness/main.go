package main

import (
	"fmt"
	"os"
	"path/filepath"

	tokenlogger "github.com/ayeshLK/komposyn/plugins/library/token-logger"
	"github.com/spf13/cobra"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/mcpworkspace"
	"github.com/ayeshLK/komposyn/internal/harness/observe"
	anthro "github.com/ayeshLK/komposyn/internal/harness/provider/anthropic"
	oai "github.com/ayeshLK/komposyn/internal/harness/provider/openai"
	rt "github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/internal/harness/store"
	tviewui "github.com/ayeshLK/komposyn/internal/harness/ui/tview"
)

func main() {
	if e := newCommand().Execute(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func newCommand() *cobra.Command {
	var dataDir, workspace string
	cmd := &cobra.Command{Use: "komposyn-harness", Short: "Local read-only AI task harness", RunE: func(cmd *cobra.Command, args []string) error {
		if workspace == "" {
			workspace = "."
		}
		workspace, e := filepath.Abs(workspace)
		if e != nil {
			return e
		}
		if dataDir == "" {
			dataDir = os.Getenv("KOMPOSYN_HARNESS_DATA_DIR")
		}
		if dataDir == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return e
			}
			dataDir = filepath.Join(home, ".local", "share", "komposyn-harness")
		}
		if e := store.EnsurePrivateDir(dataDir); e != nil {
			return e
		}
		ws, e := mcpworkspace.New(workspace)
		if e != nil {
			return e
		}
		defer ws.Close()
		mc, e := mcpworkspace.Connect(cmd.Context(), ws)
		if e != nil {
			return e
		}
		defer mc.Close()
		db, e := store.Open(filepath.Join(dataDir, "harness.db"))
		if e != nil {
			return e
		}
		defer db.Close()
		usageStore, e := tokenlogger.Open(filepath.Join(dataDir, "usage.db"))
		if e != nil {
			return fmt.Errorf("open token-only usage store: %w", e)
		}
		defer usageStore.Close()
		resolver := auth.EnvironmentResolver{}
		openai := oai.Provider{Credentials: resolver}
		anthropic := anthro.Provider{Credentials: resolver}
		runtime, e := rt.Open(db, mc, observe.TokenLogger{Store: usageStore}, openai, anthropic)
		if e != nil {
			return e
		}
		return tviewui.Run(runtime, ws.RootPath())
	}}
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "private harness data directory (default: ~/.local/share/komposyn-harness)")
	cmd.Flags().StringVar(&workspace, "workspace", ".", "read-only project workspace root")
	return cmd
}
