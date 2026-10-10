## Context

`cmd/pmox/spinner.go` had a `stepSpinner` used by launch, clone, delete
and template create. It drew its first frame immediately, so a fast
step flashed and vanished, and it lived in `package main`, out of reach
of `internal/vm`'s picker.

## Decisions

### D1. One spinner in internal/tui

`tui.Spinner` holds the drawing loop. `tui.StartSpinner(label)` returns
nil unless stderr is a terminal and spinners are on; every method is a
no-op on nil, so call sites don't branch. `stepSpinner` (the
`launch.Progress` / `template.Progress` implementation) wraps it.

### D2. Delay before drawing

The first frame waits `SpinnerDelay` (250 ms). If `Stop` comes first,
nothing was drawn and nothing is cleared. A result line from `Succeed`
is still printed, because it's the outcome, not a flash.

### D3. Off for verbose and debug

`main` turns spinners off for `-v` and `--debug`: their log lines would
interleave with the redrawn line. JSON output keeps stdout clean
anyway, since the spinner writes to stderr.

### D4. Result lines

A command's result stays on stdout, so `pmox start x | tee log` still
captures it. When a spinner was running, `finishSpin` stops it (clearing
its stderr line) and prints the result to stdout with a "✓" prefix; with
no spinner (piped stderr, verbose) the same line is printed without the
"✓". Intermediate steps of multi-step commands (launch, delete) keep
their "✓ step" lines on stderr.

### D5. "Already" checks

Commands check the current state first where that is cheap (start and
stop read the VM status they already fetch). They also recognise the
Proxmox error text for the race where the state changes between the
check and the task.
