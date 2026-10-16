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
	"fmt"
	"io"
	"strings"
)

var publicCLICommands = []string{
	"help", "version", "import", "usage", "cost", "projects", "sessions",
	"watch", "settings", "report-issue", "harness", "completion",
}

var publicWatchCommands = []string{"add", "remove", "list", "start", "status", "stop", "foreground"}

var cliHelpTopics = map[string]string{
	"": `komposyn-lg — local usage and cost reports for AI coding clients

Usage:
  komposyn-lg                         Open the interactive report view
  komposyn-lg <command> [arguments]
  komposyn-lg help <command> [subcommand]

First run:
  komposyn-lg import <file-or-directory>
  komposyn-lg usage --format table
  komposyn-lg                         Browse projects, sessions, and models

Commands:
  import          Import Codex or Claude Code transcript files
  usage, cost     Report token usage and cost estimates
  projects        List or show project resources
  sessions        Report sessions, or list/show session resources
  watch           Register sources and run foreground or detached collection
  settings        Set embedded collector preferences
  report-issue    Export pending sanitized diagnostics for review
  harness         Run the versioned JSONL integration protocol
  version         Print the embedded CLI version
  completion      Print shell completion for bash, zsh, fish, or powershell
  help            Show command help

Common options:
  --data-dir <path>   Select the local database directory where supported
  --format <format>   Select table, json, or csv where supported
  -h, --help          Show help without opening the database or starting work

Data directory: explicit --data-dir, then TOKEN_LOGGER_DATA_DIR, then the
platform default with legacy-directory compatibility. Help and completion do
not resolve a data directory, open a database, or require a home directory.

Exit status: 0 success; 1 invalid command or arguments; 2 setup, database, or
output failure; 3 incomplete collection/import outcome or watch-stop timeout.
Details: https://github.com/ayeshLK/komposyn/blob/master/docs/cli-reference.md
`,
	"help": `Usage:
  komposyn-lg help [command [subcommand]]
  komposyn-lg <command> --help

Shows command help on stdout and exits 0. Help never opens the database,
starts collection, runs onboarding, or resolves the user's home directory.
Use komposyn-lg --help for the full command list.
`,
	"version": `Usage:
  komposyn-lg version
  komposyn-lg --version

Prints the version embedded in the executable. Does not open the database.
`,
	"import": `Usage:
  komposyn-lg import <file-or-directory> [--data-dir <path>] [--format text|json]

Imports supported Codex or Claude Code transcript files. Directories are
searched recursively. Repeating an import is safe; stable ingestion keys
deduplicate already imported records. Provider files are read-only.

Options:
  --data-dir <path>    Database directory (default: TOKEN_LOGGER_DATA_DIR or platform default)
  --format text|json   Result format (default: text)

Exit status: 0 complete, 1 invalid arguments, 2 setup/I/O failure, 3 warning
outcomes may mean accounting is incomplete. A retryable incomplete final line
is reported as pending and does not alone produce status 3.

Example:
  komposyn-lg import ~/.codex/sessions --format json
`,
	"usage": `Usage:
  komposyn-lg usage [options]

Reports token usage and cost measures. Default range is the previous seven
days ending at the current UTC time. Default grouping is client, provider,
model; default format is table.

Options:
  --data-dir <path>       Database directory
  --format table|json|csv Output format (default: table)
  --from <date|RFC3339>   Inclusive start (default: now minus seven days)
  --to <date|RFC3339>     Exclusive end (default: now)
  --project <id>          Filter by stable project ID
  --session <id>          Filter by stable session ID
  --client <name>         Filter by client
  --provider <name>       Filter by provider
  --model <name>          Filter by model
  --group-by <dimensions> Comma-separated groups, such as client,model or hour
  --timezone <IANA name>  Timezone for time buckets (default: UTC)

Exit status: 0 success, 1 invalid arguments, 2 setup/database/output failure.
Use JSON or CSV for exact token integers and machine-readable fields.

Example:
  komposyn-lg usage --from 2026-10-01 --to 2026-10-08 --group-by client,model
`,
	"cost": `Usage:
  komposyn-lg cost [options]

Uses the same filters, defaults, formats, and exit statuses as usage.
Provider-reported costs remain distinct from offline calculated estimates;
unknown pricing is not treated as zero.

Example:
  komposyn-lg cost --provider openai --format csv
`,
	"projects": `Usage:
  komposyn-lg projects list [--from <time>] [--to <time>] [--data-dir <path>] [--format table|json|csv]
  komposyn-lg projects show <project-id> [--from <time>] [--to <time>] [--data-dir <path>] [--format table|json]

Project ranges default to the previous seven days ending now. List accepts
table, JSON, or CSV; show accepts table or JSON. IDs are stable IDs, not row
numbers. Exit status: 0 success, 1 invalid arguments, 2 database/output error.

Example:
  komposyn-lg projects list --format json
`,
	"projects list": `Usage:
  komposyn-lg projects list [--from <date|RFC3339>] [--to <date|RFC3339>]
    [--data-dir <path>] [--format table|json|csv]

Shows projects active in the range (default: previous seven days to now).
Exit status: 0 success, 1 invalid arguments, 2 database/output error.
`,
	"projects show": `Usage:
  komposyn-lg projects show <project-id> [--from <date|RFC3339>]
    [--to <date|RFC3339>] [--data-dir <path>] [--format table|json]

Shows one stable project ID and its summary. Range defaults to the previous
seven days ending now. Exit status: 0 success, 1 invalid arguments, 2 database
or output error.
`,
	"sessions": `Usage:
  komposyn-lg sessions [report options]
  komposyn-lg sessions list --project <project-id> [resource options]
  komposyn-lg sessions show <session-id> [--project <project-id>] [resource options]

The no-subcommand form is a compatibility report grouped by session. Resource
commands use stable IDs. Run 'komposyn-lg help sessions list' or '... show'
for details. Report options are the same as usage.
`,
	"sessions list": `Usage:
  komposyn-lg sessions list --project <project-id> [--from <time>] [--to <time>]
    [--data-dir <path>] [--format table|json|csv]

Lists sessions scoped to one project. Range defaults to the previous seven
days ending now. Exit status: 0 success, 1 invalid arguments, 2 database/output
error.
`,
	"sessions show": `Usage:
  komposyn-lg sessions show <session-id> [--project <project-id>]
    [--group-by model] [--from <time>] [--to <time>] [--client <name>]
    [--provider <name>] [--model <name>] [--data-dir <path>]
    [--format table|json|csv]

Shows a session and its per-model usage. Range defaults to the previous seven
days ending now. --project verifies session ownership. Exit status: 0 success,
1 invalid arguments, 2 database/output error.
`,
	"watch": `Usage:
  komposyn-lg watch <subcommand> [arguments]
  komposyn-lg watch <source> [<source>...] [--interval <duration>] [--data-dir <path>]

Subcommands: add, remove, list, start, status, stop, foreground. Run
'komposyn-lg help watch <subcommand>' for command-specific options. The
internal detached child command is not public. Poll interval defaults to the
saved collector setting (five seconds in a new data directory).
`,
	"watch add": `Usage:
  komposyn-lg watch add <source> [<source>...] [--defaults] [--data-dir <path>]

Registers roots for detached watch start. --defaults adds platform default
Codex and Claude roots, including roots that do not exist yet. Registration is
idempotent. Exit status: 0 success, 1 invalid source/arguments, 2 database
failure.
`,
	"watch remove": `Usage:
  komposyn-lg watch remove <source-or-id> [<source-or-id>...] [--data-dir <path>]

Disables registered roots without deleting provider files or stored history.
Exit status: 0 success, 1 invalid arguments, 2 database failure.
`,
	"watch list": `Usage:
  komposyn-lg watch list [--format text|json] [--data-dir <path>]

Lists registered sources. JSON uses format_version 1. Exit status: 0 success,
1 invalid arguments, 2 database/output failure.
`,
	"watch start": `Usage:
  komposyn-lg watch start [--interval <duration>] [--data-dir <path>]

Starts one detached collector for enabled registered sources and returns after
readiness. At least one enabled source is required. The default interval is
the saved setting (five seconds for a new data directory); an explicit
--interval also saves that preference. Exit status: 0 started/already active,
1 invalid arguments or no enabled sources, 2 startup failure.
`,
	"watch status": `Usage:
  komposyn-lg watch status [--format text|json] [--data-dir <path>]

Shows detached, foreground, or embedded collector state. JSON exposes the
versioned status shape. Exit status: 0 success, 1 invalid arguments,
2 database/output failure.
`,
	"watch stop": `Usage:
  komposyn-lg watch stop [--timeout <duration>] [--data-dir <path>]

Requests a cooperative stop and waits for the current atomic import to finish.
Timeout defaults to 10s. A timeout does not signal the stored PID and returns
status 3. Exit status: 0 stopped/already stopped, 1 invalid arguments,
2 database failure, 3 timeout.
`,
	"watch foreground": `Usage:
  komposyn-lg watch foreground [<source>...] [--interval <duration>] [--data-dir <path>]
  komposyn-lg watch <source> [<source>...] [--interval <duration>] [--data-dir <path>]

Runs collection in the foreground until interrupted. Explicit roots are
ephemeral; when none are supplied, foreground uses enabled registered roots.
The default interval is the saved setting (five seconds in a new data
directory). Ctrl-C is a normal stop. Exit status: 0 stopped cleanly,
1 invalid arguments/no roots, 2 setup or active-collector conflict,
3 incomplete collection outcomes.
`,
	"settings": `Usage:
  komposyn-lg settings [--data-dir <path>] [--collector on|off] [--auto-start on|off]

Displays and/or updates embedded interactive collector preferences. auto-start
only controls collection embedded in the REPL; detached collection starts
only through watch start. With no preference options, prints current values.
Exit status: 0 success, 1 invalid arguments/value, 2 settings/database error.

Example:
  komposyn-lg settings --collector on --auto-start off
`,
	"report-issue": `Usage:
  komposyn-lg report-issue --output <path> [--data-dir <path>]

Exports pending locally sanitized diagnostics to a new ZIP archive. It never
overwrites the destination, sends network requests, or marks diagnostics as
reviewed. Review the archive before sharing it. Exit status: 0 success,
1 invalid arguments, 2 database/archive error.
`,
	"harness": `Usage:
  komposyn-lg harness

Runs the versioned JSONL protocol on stdin/stdout. Stdout contains protocol
responses only; diagnostics go to stderr. Methods are initialize, record,
flush, health, and shutdown. See docs/HARNESS_PROTOCOL.md.
`,
	"completion": `Usage:
  komposyn-lg completion <bash|zsh|fish|powershell>

Prints deterministic, network-free shell completion source to stdout. Save the
output to the shell's completion directory or source it in your shell setup.
Use 'komposyn-lg completion --help' for this usage.
`,
}

func runHelp(args []string, stdout, stderr io.Writer) int {
	target := []string{}
	if len(args) > 0 && args[0] == "help" {
		args = args[1:]
		for _, arg := range args {
			if arg == "--help" || arg == "-h" {
				break
			}
			target = append(target, arg)
		}
	} else if len(args) > 0 && args[0] != "--help" && args[0] != "-h" {
		target = helpTargetFromCommand(args)
	}
	key := strings.Join(target, " ")
	if text, ok := cliHelpTopics[key]; ok {
		_, _ = io.WriteString(stdout, text)
		return 0
	}
	fmt.Fprintf(stderr, "unknown help topic %q; run komposyn-lg --help\n", key)
	return 1
}

func helpTargetFromCommand(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	command := args[0]
	if len(args) > 1 {
		subcommand := args[1]
		switch command {
		case "projects":
			if subcommand == "list" || subcommand == "show" {
				return []string{command, subcommand}
			}
		case "sessions":
			if subcommand == "list" || subcommand == "show" {
				return []string{command, subcommand}
			}
		case "watch":
			if containsString(publicWatchCommands, subcommand) {
				return []string{command, subcommand}
			}
		}
	}
	return []string{command}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func runCompletion(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: komposyn-lg completion <bash|zsh|fish|powershell>")
		return 1
	}
	var script string
	switch args[0] {
	case "bash":
		script = bashCompletion
	case "zsh":
		script = zshCompletion
	case "fish":
		script = fishCompletion
	case "powershell":
		script = powershellCompletion
	default:
		fmt.Fprintf(stderr, "unsupported shell %q; choose bash, zsh, fish, or powershell\n", args[0])
		return 1
	}
	_, err := io.WriteString(stdout, script)
	if err != nil {
		fmt.Fprintf(stderr, "write completion: %v\n", err)
		return 2
	}
	return 0
}

const bashCompletion = `# komposyn-lg completion for Bash
_komposyn_lg() {
  local cur prev cmd
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  cmd="${COMP_WORDS[1]}"
  if [[ ${COMP_CWORD} -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "help version import usage cost projects sessions watch settings report-issue harness completion --help -h --version" -- "$cur") )
  elif [[ "$prev" == "--format" ]]; then
    COMPREPLY=( $(compgen -W "table text json csv" -- "$cur") )
  elif [[ "$cmd" == "watch" && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=( $(compgen -W "add remove list start status stop foreground" -- "$cur") )
  elif [[ "$cmd" == "projects" && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=( $(compgen -W "list show" -- "$cur") )
  elif [[ "$cmd" == "sessions" && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=( $(compgen -W "list show" -- "$cur") )
  elif [[ "$cmd" == "completion" && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=( $(compgen -W "bash zsh fish powershell" -- "$cur") )
  else
    COMPREPLY=( $(compgen -W "--help -h --version --data-dir --format --from --to --project --session --client --provider --model --group-by --timezone --interval --timeout --defaults --collector --auto-start --output" -- "$cur") )
  fi
}
complete -F _komposyn_lg komposyn-lg
`

const zshCompletion = `#compdef komposyn-lg

_komposyn-lg() {
  local -a commands
  commands=(
    'help:Show help'
    'version:Print version'
    'import:Import transcript files'
    'usage:Report token usage'
    'cost:Report cost estimates'
    'projects:Browse projects'
    'sessions:Report or browse sessions'
    'watch:Manage collection'
    'settings:Set collector preferences'
    'report-issue:Export sanitized diagnostics'
    'harness:Run JSONL protocol'
    'completion:Print shell completion'
  )
  if (( CURRENT == 2 )); then
    _describe 'command' commands
  elif [[ ${words[2]} == watch && CURRENT == 3 ]]; then
    compadd add remove list start status stop foreground
  elif [[ ${words[2]} == projects || ${words[2]} == sessions ]] && (( CURRENT == 3 )); then
    compadd list show
  elif [[ ${words[2]} == completion && CURRENT == 3 ]]; then
    compadd bash zsh fish powershell
  else
    compadd -- --help -h --version --data-dir --format --from --to --project --session --client --provider --model --group-by --timezone --interval --timeout --defaults --collector --auto-start --output
  fi
}
`

const fishCompletion = `# komposyn-lg completion for Fish
complete -c komposyn-lg -f -n '__fish_use_subcommand' -a 'help version import usage cost projects sessions watch settings report-issue harness completion'
complete -c komposyn-lg -f -n '__fish_seen_subcommand_from watch; and not __fish_seen_subcommand_from add remove list start status stop foreground' -a 'add remove list start status stop foreground'
complete -c komposyn-lg -f -n '__fish_seen_subcommand_from projects sessions; and not __fish_seen_subcommand_from list show' -a 'list show'
complete -c komposyn-lg -f -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish powershell'
complete -c komposyn-lg -l help -s h -d 'Show help'
complete -c komposyn-lg -l version -d 'Print version'
complete -c komposyn-lg -l data-dir -r -d 'Database directory'
complete -c komposyn-lg -l format -r -a 'table text json csv' -d 'Output format'
complete -c komposyn-lg -l from -r -d 'Inclusive start time'
complete -c komposyn-lg -l to -r -d 'Exclusive end time'
complete -c komposyn-lg -l project -r -d 'Project ID'
complete -c komposyn-lg -l session -r -d 'Session ID'
complete -c komposyn-lg -l client -r -d 'Client filter'
complete -c komposyn-lg -l provider -r -d 'Provider filter'
complete -c komposyn-lg -l model -r -d 'Model filter'
complete -c komposyn-lg -l group-by -r -d 'Grouping dimensions'
complete -c komposyn-lg -l timezone -r -d 'IANA timezone'
complete -c komposyn-lg -l interval -r -d 'Polling interval'
complete -c komposyn-lg -l timeout -r -d 'Stop timeout'
complete -c komposyn-lg -l defaults -d 'Register platform defaults'
complete -c komposyn-lg -l collector -r -a 'on off' -d 'Embedded collector preference'
complete -c komposyn-lg -l auto-start -r -a 'on off' -d 'REPL collector auto-start preference'
complete -c komposyn-lg -l output -r -d 'Output archive path'
`

const powershellCompletion = `# komposyn-lg completion for PowerShell
Register-ArgumentCompleter -Native -CommandName komposyn-lg,komposyn-lg.exe -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $words = @($commandAst.CommandElements | ForEach-Object { $_.ToString() })
  $commands = @('help','version','import','usage','cost','projects','sessions','watch','settings','report-issue','harness','completion')
  $watch = @('add','remove','list','start','status','stop','foreground')
  $resources = @('list','show')
  $options = @('--help','-h','--version','--data-dir','--format','--from','--to','--project','--session','--client','--provider','--model','--group-by','--timezone','--interval','--timeout','--defaults','--collector','--auto-start','--output')
  $candidates = $options
  if ($words.Count -le 1) { $candidates = $commands }
  elseif ($words.Count -eq 2) {
    if ($words[1] -eq 'watch') { $candidates = $watch }
    elseif ($words[1] -eq 'projects' -or $words[1] -eq 'sessions') { $candidates = $resources }
    elseif ($words[1] -eq 'completion') { $candidates = @('bash','zsh','fish','powershell') }
    elseif ($words[1] -notin $commands) { $candidates = $commands }
  }
  elseif ($words.Count -eq 3 -and $words[1] -eq 'watch' -and $words[2] -notin $watch) { $candidates = $watch }
  elseif ($words.Count -eq 3 -and ($words[1] -eq 'projects' -or $words[1] -eq 'sessions') -and $words[2] -notin $resources) { $candidates = $resources }
  elseif ($words.Count -eq 3 -and $words[1] -eq 'completion') { $candidates = @('bash','zsh','fish','powershell') }
  $candidates | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
}
`
