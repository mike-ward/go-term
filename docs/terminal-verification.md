# Terminal Verification

This project verifies terminal-emulator behavior in three layers:

1. Pure unit tests for grid, parser, PTY, and widget helpers.
2. Replay tests that feed realistic escape streams and assert the final
   screen state.
3. Manual compatibility checks in `cmd/demo` for GUI-only behavior.

## Automated Suites

Run the full automated suite:

```bash
go test ./...
go test -race ./...
go vet ./...
```

Run only the replay-style emulator checks:

```bash
go test ./term -run EmulatorReplay
```

## Capability Matrix

| Capability | Verification |
| --- | --- |
| Plain text, CR/LF/BS/TAB, UTF-8 decode | `parser_test.go`, `grid_test.go` |
| Cursor movement and erase operations | `parser_test.go`, `emulator_replay_test.go` |
| SGR attributes, 16-color, 256-color, truecolor | `parser_test.go`, `palette_test.go` |
| Scroll regions, insert/delete line/char, IND/RI/NEL | `grid_test.go`, `parser_test.go` |
| Alt screen save/restore | `grid_test.go`, `parser_test.go`, `emulator_replay_test.go` |
| OSC title and OSC 7 working-directory updates | `parser_test.go`, `emulator_replay_test.go` |
| Device replies (`DA1`, `DECRQSS`, `XTGETTCAP`) | `parser_test.go`, `emulator_replay_test.go` |
| Bracketed paste, focus reporting, mouse modes, sync output | `parser_test.go`, `widget_test.go`, `emulator_replay_test.go` |
| PTY startup and resize plumbing | `pty_test.go` |
| GUI-only selection, scrolling, clipboard, redraw behavior | `widget_test.go` plus manual demo runs |

## Manual Checks

Start the demo:

```bash
cd cmd/demo
go run .
```

Exercise these behaviors in the embedded shell:

```bash
printf 'plain\ntext\n'
printf '\x1b[31mred\x1b[0m \x1b[38;5;82mgreen256\x1b[0m \x1b[38;2;255;100;0mtruecolor\x1b[0m\n'
printf 'line1\nline2\nline3\nline4\nline5\n'
vim README.md
less README.md
```

Validate:

| Behavior | What to check |
| --- | --- |
| Resize | `stty size` changes after window resize |
| Scrollback | mouse wheel and PgUp/PgDn move through history |
| Selection/copy | drag-select copies trimmed text |
| Paste | multi-line paste does not auto-execute in bracketed paste mode |
| Alt screen | `vim` and `less` restore the main buffer on exit |
| Mouse/focus | mouse-aware apps and focus events do not leak garbage text |

## External Conformance Tools

This repo does not bundle a full external terminal conformance suite.
For broader compatibility work, use:

- `vttest` for classic VT/xterm behavior
- `tic`, `infocmp`, and `tput` for terminfo validation
- real application checks with `vim`, `less`, `tmux`, `htop`, and shell
  line editing

Treat those as complementary to the Go tests here. They catch integration
gaps that are hard to model with parser-only assertions.
