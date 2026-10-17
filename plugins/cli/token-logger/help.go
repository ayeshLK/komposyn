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
  local cur prev cmd subcmd formats options
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  cmd="${COMP_WORDS[1]}"
  subcmd="${COMP_WORDS[2]}"
  if [[ "$cmd" == "sessions" && "$subcmd" != "list" && "$subcmd" != "show" ]]; then subcmd=""; fi
  if [[ ${COMP_CWORD} -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "help version import usage cost projects sessions watch settings report-issue harness completion --help -h --version" -- "$cur") )
  elif [[ "$cmd" == "watch" && ${COMP_CWORD} -eq 2 ]]; then
    if [[ "$cur" == -* ]]; then
      COMPREPLY=( $(compgen -W "--help -h --data-dir --interval" -- "$cur") )
    else
      COMPREPLY=( $(compgen -W "add remove list start status stop foreground" -- "$cur") )
    fi
  elif [[ "$cmd" == "projects" && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=( $(compgen -W "list show" -- "$cur") )
  elif [[ "$cmd" == "sessions" && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=( $(compgen -W "list show" -- "$cur") )
  elif [[ "$cmd" == "completion" && ${COMP_CWORD} -eq 2 ]]; then
    COMPREPLY=( $(compgen -W "bash zsh fish powershell" -- "$cur") )
  elif [[ "$cmd" == "version" ]]; then
    options="--help -h --version"
    COMPREPLY=( $(compgen -W "$options" -- "$cur") )
  elif [[ "$prev" == "--format" ]]; then
    case "$cmd:$subcmd" in
      import:*) formats="text json" ;;
      usage:*|cost:*|sessions:*) formats="table json csv" ;;
      projects:list) formats="table json csv" ;;
      projects:show) formats="table json" ;;
      watch:list|watch:status) formats="text json" ;;
      *) formats="" ;;
    esac
    COMPREPLY=( $(compgen -W "$formats" -- "$cur") )
  elif [[ "$prev" == "--collector" || "$prev" == "--auto-start" ]]; then
    COMPREPLY=( $(compgen -W "on off" -- "$cur") )
  else
    case "$cmd:$subcmd" in
      import:*) options="--help -h --data-dir --format" ;;
      usage:*|cost:*|sessions:) options="--help -h --data-dir --format --from --to --project --session --client --provider --model --group-by --timezone" ;;
      projects:list) options="--help -h --data-dir --format --from --to --client --provider --model" ;;
      projects:show) options="--help -h --data-dir --format --from --to --client --provider --model" ;;
      sessions:list) options="--help -h --data-dir --format --from --to --project --client --provider --model" ;;
      sessions:show) options="--help -h --data-dir --format --from --to --project --client --provider --model --group-by" ;;
      watch:add) options="--help -h --data-dir --defaults" ;;
      watch:remove) options="--help -h --data-dir" ;;
      watch:list|watch:status) options="--help -h --data-dir --format" ;;
      watch:stop) options="--help -h --data-dir --timeout" ;;
      watch:start|watch:foreground|watch:*) options="--help -h --data-dir --interval" ;;
      settings:*) options="--help -h --data-dir --collector --auto-start" ;;
      report-issue:*) options="--help -h --data-dir --output" ;;
      version:*) options="--help -h --version" ;;
      *) options="--help -h" ;;
    esac
    if [[ "$cmd" == "version" || "$cmd" == "help" || "$cmd" == "harness" || "$cmd" == "completion" ]]; then
      options="--help -h"
    fi
    case "$prev" in
      --data-dir|--from|--to|--project|--session|--client|--provider|--model|--group-by|--timezone|--interval|--timeout|--output) options="" ;;
    esac
    COMPREPLY=( $(compgen -W "$options" -- "$cur") )
  fi
}
complete -o default -F _komposyn_lg komposyn-lg
`

const zshCompletion = `#compdef komposyn-lg

_komposyn-lg() {
  local -a commands
  local command subcommand
  command="${words[2]}"
  subcommand="${words[3]}"
  if [[ $command == sessions && $subcommand != list && $subcommand != show ]]; then subcommand=""; fi
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
    '--help:Show help'
    '-h:Show help'
    '--version:Print version'
  )
  if (( CURRENT == 2 )); then
    _describe 'command' commands
  elif [[ ${words[2]} == watch ]] && (( CURRENT == 3 )); then
    if [[ ${words[CURRENT]} == -* ]]; then
      compadd -- --help -h --data-dir --interval
    else
      compadd add remove list start status stop foreground
    fi
  elif [[ ${words[2]} == projects || ${words[2]} == sessions ]] && (( CURRENT == 3 )); then
    compadd list show
  elif [[ ${words[2]} == completion ]] && (( CURRENT == 3 )); then
    compadd bash zsh fish powershell
  elif [[ ${words[CURRENT-1]} == --format ]]; then
    case "$command:$subcommand" in
      import:*) compadd text json ;;
      usage:*|cost:*|sessions:*) compadd table json csv ;;
      projects:list) compadd table json csv ;;
      projects:show) compadd table json ;;
      watch:list|watch:status) compadd text json ;;
    esac
  elif [[ ${words[CURRENT-1]} == --collector || ${words[CURRENT-1]} == --auto-start ]]; then
    compadd on off
  elif [[ ${words[CURRENT-1]} == --data-dir || ${words[CURRENT-1]} == --from || ${words[CURRENT-1]} == --to || ${words[CURRENT-1]} == --project || ${words[CURRENT-1]} == --session || ${words[CURRENT-1]} == --client || ${words[CURRENT-1]} == --provider || ${words[CURRENT-1]} == --model || ${words[CURRENT-1]} == --group-by || ${words[CURRENT-1]} == --timezone || ${words[CURRENT-1]} == --interval || ${words[CURRENT-1]} == --timeout || ${words[CURRENT-1]} == --output ]]; then
    return
  else
    case "$command:$subcommand" in
      import:*) compadd -- --help -h --data-dir --format ;;
      usage:*|cost:*|sessions:) compadd -- --help -h --data-dir --format --from --to --project --session --client --provider --model --group-by --timezone ;;
      projects:list|projects:show) compadd -- --help -h --data-dir --format --from --to --client --provider --model ;;
      sessions:list) compadd -- --help -h --data-dir --format --from --to --project --client --provider --model ;;
      sessions:show) compadd -- --help -h --data-dir --format --from --to --project --client --provider --model --group-by ;;
      watch:add) compadd -- --help -h --data-dir --defaults ;;
      watch:remove) compadd -- --help -h --data-dir ;;
      watch:list|watch:status) compadd -- --help -h --data-dir --format ;;
      watch:stop) compadd -- --help -h --data-dir --timeout ;;
      watch:start|watch:foreground|watch:*) compadd -- --help -h --data-dir --interval ;;
      version:*) compadd -- --help -h --version ;;
      settings:*) compadd -- --help -h --data-dir --collector --auto-start ;;
      report-issue:*) compadd -- --help -h --data-dir --output ;;
      *) compadd -- --help -h ;;
    esac
  fi
}
`

const fishCompletion = `# komposyn-lg completion for Fish
complete -c komposyn-lg -f -n '__fish_use_subcommand' -a 'help version import usage cost projects sessions watch settings report-issue harness completion'
complete -c komposyn-lg -f -n '__fish_seen_subcommand_from watch; and not __fish_seen_subcommand_from add remove list start status stop foreground' -a 'add remove list start status stop foreground'
complete -c komposyn-lg -f -n '__fish_seen_subcommand_from projects sessions; and not __fish_seen_subcommand_from list show' -a 'list show'
complete -c komposyn-lg -f -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish powershell'
complete -c komposyn-lg -l help -s h -d 'Show help'
complete -c komposyn-lg -l version -n '__fish_use_subcommand' -d 'Print version'
complete -c komposyn-lg -l version -n '__fish_seen_subcommand_from version' -d 'Print version'
complete -c komposyn-lg -l data-dir -r -n '__fish_seen_subcommand_from import usage cost sessions projects watch settings report-issue' -d 'Database directory'
complete -c komposyn-lg -l format -r -a 'text json' -n '__fish_seen_subcommand_from import' -d 'Import result format'
complete -c komposyn-lg -l format -r -a 'table json csv' -n '__fish_seen_subcommand_from usage cost sessions' -d 'Report format'
complete -c komposyn-lg -l format -r -a 'table json csv' -n '__fish_seen_subcommand_from projects; and __fish_seen_subcommand_from list' -d 'Project list format'
complete -c komposyn-lg -l format -r -a 'table json' -n '__fish_seen_subcommand_from projects; and __fish_seen_subcommand_from show' -d 'Project detail format'
complete -c komposyn-lg -l format -r -a 'table json csv' -n '__fish_seen_subcommand_from sessions; and __fish_seen_subcommand_from list show' -d 'Session format'
complete -c komposyn-lg -l format -r -a 'table json csv' -n '__fish_seen_subcommand_from sessions; and __fish_seen_subcommand_from show' -d 'Session detail format'
complete -c komposyn-lg -l format -r -a 'text json' -n '__fish_seen_subcommand_from watch; and __fish_seen_subcommand_from list status' -d 'Watch output format'
complete -c komposyn-lg -l from -r -n '__fish_seen_subcommand_from usage cost sessions projects' -d 'Inclusive start time'
complete -c komposyn-lg -l to -r -n '__fish_seen_subcommand_from usage cost sessions projects' -d 'Exclusive end time'
complete -c komposyn-lg -l project -r -n '__fish_seen_subcommand_from usage cost' -d 'Project ID'
complete -c komposyn-lg -l project -r -n '__fish_seen_subcommand_from sessions; and not __fish_seen_subcommand_from list show' -d 'Project ID'
complete -c komposyn-lg -l project -r -n '__fish_seen_subcommand_from sessions; and __fish_seen_subcommand_from list show' -d 'Project ID'
complete -c komposyn-lg -l session -r -n '__fish_seen_subcommand_from usage cost' -d 'Session ID'
complete -c komposyn-lg -l session -r -n '__fish_seen_subcommand_from sessions; and not __fish_seen_subcommand_from list show' -d 'Session ID'
complete -c komposyn-lg -l client -r -n '__fish_seen_subcommand_from usage cost sessions projects' -d 'Client filter'
complete -c komposyn-lg -l provider -r -n '__fish_seen_subcommand_from usage cost sessions projects' -d 'Provider filter'
complete -c komposyn-lg -l model -r -n '__fish_seen_subcommand_from usage cost sessions projects' -d 'Model filter'
complete -c komposyn-lg -l group-by -r -n '__fish_seen_subcommand_from usage cost' -d 'Grouping dimensions'
complete -c komposyn-lg -l group-by -r -n '__fish_seen_subcommand_from sessions; and not __fish_seen_subcommand_from list show' -d 'Grouping dimensions'
complete -c komposyn-lg -l group-by -r -a 'model' -n '__fish_seen_subcommand_from sessions; and __fish_seen_subcommand_from show' -d 'Session grouping'
complete -c komposyn-lg -l timezone -r -n '__fish_seen_subcommand_from usage cost' -d 'IANA timezone'
complete -c komposyn-lg -l timezone -r -n '__fish_seen_subcommand_from sessions; and not __fish_seen_subcommand_from list show' -d 'IANA timezone'
complete -c komposyn-lg -l interval -r -n '__fish_seen_subcommand_from watch; and __fish_seen_subcommand_from start foreground' -d 'Polling interval'
complete -c komposyn-lg -l interval -r -n '__fish_seen_subcommand_from watch; and not __fish_seen_subcommand_from add remove list start status stop foreground' -d 'Polling interval'
complete -c komposyn-lg -l timeout -r -n '__fish_seen_subcommand_from watch; and __fish_seen_subcommand_from stop' -d 'Stop timeout'
complete -c komposyn-lg -l defaults -n '__fish_seen_subcommand_from watch; and __fish_seen_subcommand_from add' -d 'Register platform defaults'
complete -c komposyn-lg -l collector -r -a 'on off' -n '__fish_seen_subcommand_from settings' -d 'Embedded collector preference'
complete -c komposyn-lg -l auto-start -r -a 'on off' -n '__fish_seen_subcommand_from settings' -d 'REPL collector auto-start preference'
complete -c komposyn-lg -l output -r -n '__fish_seen_subcommand_from report-issue' -d 'Output archive path'
`

const powershellCompletion = `# komposyn-lg completion for PowerShell
Register-ArgumentCompleter -Native -CommandName komposyn-lg,komposyn-lg.exe -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $words = @($commandAst.CommandElements | ForEach-Object { $_.ToString() })
  $commands = @('help','version','import','usage','cost','projects','sessions','watch','settings','report-issue','harness','completion')
  $watch = @('add','remove','list','start','status','stop','foreground')
  $resources = @('list','show')
  $elements = [System.Collections.Generic.List[string]]::new()
  foreach ($element in $words) { $elements.Add($element) }
  if ($wordToComplete -and $elements.Count -gt 1 -and $elements[$elements.Count - 1] -eq $wordToComplete) { $elements.RemoveAt($elements.Count - 1) }
  $previous = if ($elements.Count -gt 1) { $elements[$elements.Count - 1] } else { '' }
  $command = if ($elements.Count -gt 1) { $elements[1] } else { '' }
  $subcommand = if ($elements.Count -gt 2) { $elements[2] } else { '' }
  if ($command -eq 'sessions' -and $subcommand -notin $resources) { $subcommand = '' }
  $candidates = @('--help','-h')
  if ($previous -eq '--format') {
    switch ("${command}:${subcommand}") {
      { $_ -like 'import:*' } { $candidates = @('text','json'); break }
      { $_ -like 'usage:*' -or $_ -like 'cost:*' -or $_ -like 'sessions:*' } { $candidates = @('table','json','csv'); break }
      'projects:list' { $candidates = @('table','json','csv'); break }
      'projects:show' { $candidates = @('table','json'); break }
      { $_ -eq 'watch:list' -or $_ -eq 'watch:status' } { $candidates = @('text','json'); break }
      default { $candidates = @() }
    }
  }
  elseif ($previous -eq '--collector' -or $previous -eq '--auto-start') { $candidates = @('on','off') }
  elseif ($previous -in @('--data-dir','--from','--to','--project','--session','--client','--provider','--model','--group-by','--timezone','--interval','--timeout','--output')) { $candidates = @() }
  elseif ($command -eq 'completion' -and $elements.Count -le 2) { $candidates = @('bash','zsh','fish','powershell') }
  elseif ($command -eq 'watch' -and $elements.Count -eq 2 -and $wordToComplete -like '-*') { $candidates = @('--help','-h','--data-dir','--interval') }
  elseif ($elements.Count -le 1) { $candidates = $commands + @('--help','-h','--version') }
  elseif ($elements.Count -eq 2 -and $command -eq 'watch') { $candidates = $watch }
  elseif ($elements.Count -eq 2 -and ($command -eq 'projects' -or $command -eq 'sessions')) { $candidates = $resources }
  elseif ($elements.Count -eq 2 -and $command -notin $commands) { $candidates = $commands + @('--help','-h','--version') }
  else {
    switch ("${command}:${subcommand}") {
      { $_ -like 'import:*' } { $candidates = @('--help','-h','--data-dir','--format'); break }
      { $_ -like 'usage:*' -or $_ -like 'cost:*' -or $_ -eq 'sessions:' } { $candidates = @('--help','-h','--data-dir','--format','--from','--to','--project','--session','--client','--provider','--model','--group-by','--timezone'); break }
      'projects:list' { $candidates = @('--help','-h','--data-dir','--format','--from','--to','--client','--provider','--model'); break }
      'projects:show' { $candidates = @('--help','-h','--data-dir','--format','--from','--to','--client','--provider','--model'); break }
      'sessions:list' { $candidates = @('--help','-h','--data-dir','--format','--from','--to','--project','--client','--provider','--model'); break }
      'sessions:show' { $candidates = @('--help','-h','--data-dir','--format','--from','--to','--project','--client','--provider','--model','--group-by'); break }
      'watch:add' { $candidates = @('--help','-h','--data-dir','--defaults'); break }
      'watch:remove' { $candidates = @('--help','-h','--data-dir'); break }
      { $_ -eq 'watch:list' -or $_ -eq 'watch:status' } { $candidates = @('--help','-h','--data-dir','--format'); break }
      'watch:stop' { $candidates = @('--help','-h','--data-dir','--timeout'); break }
      { $_ -eq 'watch:start' -or $_ -eq 'watch:foreground' -or $_ -like 'watch:*' } { $candidates = @('--help','-h','--data-dir','--interval'); break }
      'settings:' { $candidates = @('--help','-h','--data-dir','--collector','--auto-start'); break }
      'report-issue:' { $candidates = @('--help','-h','--data-dir','--output'); break }
      { $_ -like 'version:*' } { $candidates = @('--help','-h','--version'); break }
      default { $candidates = @('--help','-h') }
    }
  }
  $candidates | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
}
`
