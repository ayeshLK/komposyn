# Windows console integration test

The Windows CI job runs `go test ./...` with the job runner's redirected
standard streams. The Windows-specific unit tests validate synthetic
`ReadConsoleInputW` records and screen-buffer operations, but this does not
exercise `komposyn-lg` inside a user-attached console. Do not describe that
coverage as an end-to-end console integration test.

Run this procedure in an interactive PowerShell window attached to Windows
Console Host or Windows Terminal. Do not pipe or redirect the executable's
standard input or output. A terminal host may reserve Ctrl+Enter for its own
shortcut; if so, use a console host/configuration that passes the key chord to
the application and record which host was tested.

## Build and prepare

From the repository root:

```powershell
New-Item -ItemType Directory -Force .\bin | Out-Null
go build -o .\bin\komposyn-lg.exe .\plugins\cli\token-logger
$exe = (Resolve-Path .\bin\komposyn-lg.exe).Path
$dataDir = Join-Path $env:TEMP ("komposyn-console-test-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $dataDir | Out-Null
$env:TOKEN_LOGGER_DATA_DIR = $dataDir
```

Use a new temporary data directory so first-run prompts appear and the test
does not inspect or modify an existing Komposyn profile.

Define a small helper to capture the console input mode. The application uses
raw console input while the editor is active and must restore the original
mode when it exits:

```powershell
Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;

public static class KomposynConsoleMode {
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern IntPtr GetStdHandle(int handle);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool GetConsoleMode(IntPtr handle, out uint mode);

    public static uint InputMode() {
        IntPtr handle = GetStdHandle(-10); // STD_INPUT_HANDLE
        uint mode;
        if (handle == IntPtr.Zero || handle == new IntPtr(-1) || !GetConsoleMode(handle, out mode))
            throw new Win32Exception(Marshal.GetLastWin32Error());
        return mode;
    }
}
'@
```

## Normal-exit pass

Capture the mode and launch the executable directly from that PowerShell
window:

```powershell
$beforeNormalExit = [KomposynConsoleMode]::InputMode()
& $exe
$normalExitCode = $LASTEXITCODE
$afterNormalExit = [KomposynConsoleMode]::InputMode()
if ($normalExitCode -ne 0) { throw "Normal run exited with $normalExitCode" }
if ($beforeNormalExit -ne $afterNormalExit) { throw "Console input mode was not restored" }
```

At first-run setup, answer `n` to “Start the local collector now?” and press
plain Enter at the next `[y/N]` prompt. Enter should accept the displayed
default and setup should finish.

At the REPL, perform these checks:

1. Type `/setings`, press Home, press Right four times, type `t`, then press
   Ctrl+Enter. The corrected `/settings` command should run. This exercises
   cursor movement and insertion.
2. Press Up to recall `/settings`, then Down to return to a blank command.
   This exercises in-session command history.
3. Type `/help` and press plain Enter. It must insert a space without running
   the command. Press Ctrl+Enter; the help output should then appear.
4. Run `/clear` with Ctrl+Enter and confirm the visible screen clears. Press
   Ctrl+L and confirm the screen clears again while the prompt remains usable.
5. Run `/quit` with Ctrl+Enter. The process should exit normally, the mode
   comparison above should pass, and the PowerShell prompt should remain usable.

## Ctrl+C interruption pass

Launch the same executable again, capture the mode, type part of a command,
and press Ctrl+C at the REPL prompt:

```powershell
$beforeInterrupt = [KomposynConsoleMode]::InputMode()
& $exe
$interruptExitCode = $LASTEXITCODE
$afterInterrupt = [KomposynConsoleMode]::InputMode()
if ($interruptExitCode -ne 0) { throw "Interrupted run exited with $interruptExitCode" }
if ($beforeInterrupt -ne $afterInterrupt) { throw "Console input mode was not restored after Ctrl+C" }
```

The app should leave interactive mode cleanly, restore the exact input mode,
and return control to a usable PowerShell prompt. If Ctrl+C is intercepted by
the terminal host rather than delivered to the application, note that as a
host limitation and repeat in a host that passes the key event through.

Record the Komposyn commit, Windows version, console host (and version), test
date, and pass/fail result for each check when reporting manual validation.
The automated Windows unit tests remain required; this procedure supplements
them and does not replace CI.
