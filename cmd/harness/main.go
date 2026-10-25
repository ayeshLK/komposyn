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

// Command komposyn starts the experimental text chat in this module.
package main

import (
	"fmt"
	"os"
	goruntime "runtime"

	"github.com/ayeshLK/komposyn/internal/harness/auth"
	"github.com/ayeshLK/komposyn/internal/harness/model"
	"github.com/ayeshLK/komposyn/internal/harness/runtime"
	"github.com/ayeshLK/komposyn/internal/harness/settings"
	harnessui "github.com/ayeshLK/komposyn/internal/harness/ui"
	provideranthropic "github.com/ayeshLK/komposyn/plugins/model-provider/anthropic"
	provideropenai "github.com/ayeshLK/komposyn/plugins/model-provider/openai"
	uiplugin "github.com/ayeshLK/komposyn/plugins/ui"
	tviewui "github.com/ayeshLK/komposyn/plugins/ui/tview"
	"github.com/spf13/cobra"
)

// These values are set by release builds. Local builds identify themselves as
// development builds and never infer a version from an unreviewed manifest.
var cliVersion = "dev"
var cliCommit = "unknown"

func main() {
	if err := newCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newCommand() *cobra.Command {
	var authPath, settingsPath string
	var check bool
	cmd := &cobra.Command{
		Use:     "komposyn",
		Version: fmt.Sprintf("version=%s commit=%s target=%s/%s", cliVersion, cliCommit, goruntime.GOOS, goruntime.GOARCH),
		Short:   "Start a temporary text chat with an OpenAI or Anthropic model",
		Long:    "KompoSyn runs a local experimental chat. Use /model to add a provider profile, discover models, and select one. Messages and model selection exist only until exit; credentials and UI settings are stored locally.",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profileStore := auth.Store{Path: authPath}
			settingsStore := settings.Store{Path: settingsPath}
			prefs, err := settingsStore.Load()
			if err != nil {
				return err
			}
			if check {
				profiles, err := profileStore.ListMetadata()
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "UI: %s\nConfigured provider profiles: %d\n", prefs.Plugin, len(profiles))
				for _, p := range profiles {
					fmt.Fprintf(cmd.OutOrStdout(), "- %s · %s · %s\n", p.Provider, p.Label, p.ID)
				}
				return nil
			}
			registry, err := model.NewRegistry(provideropenai.Plugin{}, provideranthropic.Plugin{})
			if err != nil {
				return err
			}
			chat := runtime.NewChat(registry, profileStore)
			frontend, err := harnessui.Load(prefs.Plugin, []uiplugin.Plugin{tviewui.New()})
			if err != nil {
				return err
			}
			return frontend.Run(cmd.Context(), harnessui.Client{Chat: chat, ProfileStore: profileStore})
		},
	}
	cmd.SetVersionTemplate("{{.Name}} {{.Version}}\n")
	cmd.Flags().StringVar(&authPath, "auth-file", "", "credential file path (default: ~/.komposyn/auth.json)")
	cmd.Flags().StringVar(&settingsPath, "settings-file", "", "settings file path (default: ~/.komposyn/settings.json)")
	cmd.Flags().BoolVar(&check, "check", false, "validate local configuration and list provider profile metadata without opening the terminal UI")
	return cmd
}
