## ADDED Requirements

### Requirement: Confirmer interface in `internal/tui`

The `internal/tui` package SHALL expose a `Confirmer` interface with a single method `Confirm(ctx context.Context, prompt string) (bool, error)` that asks the operator a yes/no question and returns the operator's answer. The interface SHALL exist so that destructive commands can inject either a real terminal-backed implementation, an `--yes` bypass implementation, or a hand-rolled fake from tests, without each command reimplementing the prompt loop.

#### Scenario: Multiple commands share one Confirmer abstraction
- **WHEN** a destructive command (e.g. `pmox delete`) needs to gate a destructive action behind a y/N prompt
- **THEN** it SHALL accept a `tui.Confirmer` value as a parameter rather than calling `os.Stdin` / `bufio.Scanner` directly
- **AND** the command's tests SHALL be able to substitute a fake `Confirmer` to drive the gate without a real TTY

### Requirement: TTY-backed Confirmer implementation

The `internal/tui` package SHALL provide `NewTTYConfirmer(in io.Reader, out io.Writer) Confirmer` which prints the supplied prompt to `out`, reads a single line from `in`, and returns `true` only when the trimmed, lower-cased input is exactly `y` or `yes`. Any other input — including the empty line — SHALL return `false`. The implementation SHALL return an error only when reading from `in` itself fails (not when the operator declines).

#### Scenario: Operator types `y` followed by enter
- **WHEN** `Confirm` is called and the underlying reader yields `"y\n"`
- **THEN** the call SHALL return `(true, nil)`

#### Scenario: Operator types `yes` followed by enter
- **WHEN** the underlying reader yields `"yes\n"`
- **THEN** the call SHALL return `(true, nil)`

#### Scenario: Operator types `Y` followed by enter
- **WHEN** the underlying reader yields `"Y\n"`
- **THEN** the call SHALL return `(true, nil)` (case-insensitive match)

#### Scenario: Operator presses enter on the empty line (default No)
- **WHEN** the underlying reader yields `"\n"`
- **THEN** the call SHALL return `(false, nil)`

#### Scenario: Operator types `n`
- **WHEN** the underlying reader yields `"n\n"`
- **THEN** the call SHALL return `(false, nil)`

#### Scenario: Operator types unrelated text
- **WHEN** the underlying reader yields `"maybe\n"`
- **THEN** the call SHALL return `(false, nil)` — anything other than `y`/`yes` is denial

#### Scenario: Reader returns I/O error
- **WHEN** the underlying reader returns a non-EOF error
- **THEN** the call SHALL return `(false, err)` so the caller can distinguish "I couldn't ask" from "the operator said no"

### Requirement: `AlwaysConfirmer` bypass implementation

The `internal/tui` package SHALL provide an `AlwaysConfirmer` (struct or value) whose `Confirm` method returns `(true, nil)` unconditionally. Destructive commands SHALL select this implementation when the user has set `--yes` or the equivalent assume-yes environment variable.

#### Scenario: AlwaysConfirmer ignores the prompt
- **WHEN** `AlwaysConfirmer{}.Confirm(ctx, "anything")` is called
- **THEN** it SHALL return `(true, nil)` without reading from any reader or writing to any writer

### Requirement: Non-TTY refusal helper

The `internal/tui` package SHALL expose a helper that destructive commands can call to detect that stdin is not a terminal AND no assume-yes bypass has been set, so they can return a clear, actionable error before reaching `Confirm`. The helper SHALL NOT itself terminate the process; the caller decides how to surface the error.

#### Scenario: Stdin is a TTY
- **WHEN** the helper is called and stdin is connected to a terminal
- **THEN** it SHALL report that interactive confirmation is possible

#### Scenario: Stdin is a pipe and bypass is unset
- **WHEN** the helper is called, stdin is not a TTY, and the assume-yes bypass is `false`
- **THEN** it SHALL report that interactive confirmation is NOT possible
- **AND** the calling command SHALL surface an error directing the user to pass `--yes` or set `PMOX_ASSUME_YES=1`

#### Scenario: Stdin is a pipe and bypass is set
- **WHEN** the helper is called, stdin is not a TTY, and the assume-yes bypass is `true`
- **THEN** the calling command SHALL skip interactive confirmation and proceed via `AlwaysConfirmer`
