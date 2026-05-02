# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Roadmap

Post-MVP feature plan with progress checkboxes lives in `ROADMAP.md`
at the repo root. Tick boxes there as work lands.

## What this is

`go-term` is a minimal terminal-emulator widget built on the `go-gui`
framework (sibling repo `../go-gui`). It spawns a real shell via PTY and
renders the cell grid through `gui.DrawCanvas`. It is intentionally
small — designed to stay approachable, not feature-complete. Targets
macOS + Linux only.

## Common commands

```bash
# Run the demo window
cd cmd/demo && go run .

# Build everything
go build ./...

# Vet
go vet ./...

# Tidy module graph
go mod tidy
```

There are no tests yet. The widget is GUI-only and cannot be validated
from a headless shell — verify visually by running `cmd/demo` and trying
`ls`, `cat`, ANSI color output, and window resize (then `stty size` in
the embedded shell).

## Local replace dependency

`go.mod` uses `replace` directives pointing at sibling working trees:

```
replace (
    github.com/mike-ward/go-glyph => ../go-glyph
    github.com/mike-ward/go-gui   => ../go-gui
)
```

Both sibling repos must be present at `../go-gui` and `../go-glyph`
relative to this repo's root. Edits in those trees are picked up
immediately by `go build`.

## Architecture

Three layers, one file each, in `term/`. Each layer is independently
testable and the dependencies flow strictly downward.

```
cmd/demo/main.go       gui.NewWindow + term.New + backend.Run
        │
        ▼
term/widget.go         Term widget: View(), OnDraw, OnChar, OnKeyDown,
                       reader goroutine. Bridge to go-gui.
        │
        ▼
term/parser.go         VT state machine. Bytes → grid mutations.
        │
        ▼
term/grid.go           Cell buffer + cursor + scroll-up. Pure data.

term/pty.go            creack/pty wrapper. Spawns $SHELL, resize ioctl.
term/palette.go        16-color ANSI table + default fg/bg.
```

### Concurrency model

- One PTY reader goroutine, started in `term.New`.
- `Grid.Mu` is the single lock. The reader goroutine takes it to feed
  the parser; `OnDraw` takes it to read cells. Never hold it across a
  go-gui call.
- After feeding bytes, the reader calls `win.QueueCommand(...)` to
  schedule a redraw on the main thread. **Never touch `*gui.Window`
  state directly from the reader goroutine** — `QueueCommand` is the
  only thread-safe path.

### Render loop

1. `OnDraw` runs on the main thread inside go-gui's frame pipeline.
2. First call: measure cell width via `dc.TextWidth("M", style)` and
   line height via `dc.FontHeight(style)`. These can return 0 before
   the backend's `TextMeasurer` is ready — the function returns early
   in that case and a later frame populates them.
3. Each frame: derive `rows = floor(dc.Height/cellH)`,
   `cols = floor(dc.Width/cellW)`. If they changed, `Grid.Resize` and
   `PTY.Resize` (sends `TIOCSWINSZ` so the child sees `SIGWINCH`).
4. Two passes per frame: coalesced bg-rect runs per row, then per-cell
   text. Cursor drawn last as inverted block.
5. The `DrawCanvas` is created with empty `ID`, which bypasses the
   tessellation cache — every frame re-runs `OnDraw`. If perf becomes
   an issue, give it an ID and bump `Version` on grid changes.

### Parser scope (intentional)

Only what's needed to display `cat`, `ls`, simple prompts, and 16-color
output. Implemented:

- C0: `BEL`, `BS`, `HT`, `LF`, `CR`, `ESC`.
- `CSI ... m` (SGR): reset, bold/underline/inverse + clears, fg 30-37
  + bright 90-97, bg 40-47 + bright 100-107, default 39/49. Truecolor
  (`38;2;...`) and 256-color (`38;5;...`) introducers are *parsed and
  swallowed* so subsequent params don't get reinterpreted as separate
  SGRs — they don't recolor.
- All other CSI sequences (cursor moves, erase-in-display, scroll
  regions, alt screen, mouse, etc.): silently dropped. UTF-8 decoded
  with carry-over across `Feed` calls.

When extending: add cases to `dispatchCSI` in `term/parser.go`. Don't
let parser code reach into go-gui — it must stay grid-only.

### Keyboard input

`onChar` (printable runes via `gui.ContainerCfg.OnChar`) writes UTF-8
to the PTY. `onKeyDown` translates non-printable keys (arrows, Enter,
Backspace, Delete, Page Up/Down, Home/End, Ctrl+letter) into terminal
byte sequences. Backspace sends `0x7F` (DEL) per xterm convention.
Set `e.IsHandled = true` so go-gui doesn't propagate.

The widget claims focus via `IDFocus: 1` on its outer `gui.Column`.
If keystrokes don't reach the PTY, focus is the first place to look.

## Out-of-scope (don't add casually)

These were excluded from MVP and should remain so unless a feature
explicitly requires them. Each is a real chunk of work:

- Alt screen / xterm 1049 (vim, htop, less, tmux all want this)
- Mouse reporting (1000/1006)
- 24-bit truecolor
- Scrollback ring buffer
- Bracketed paste
- OSC sequences (titles, hyperlinks, OSC 52 clipboard)
- IME composition / dead keys
- Sixel / kitty graphics
- Windows/ConPTY support

## Conventions

- Comments wrap at ~90 columns.
- Public API in `term/` is small on purpose: `Cfg`, `Term`, `New`,
  `View`, `Close`. Keep it that way; add unexported helpers freely.
- Performance target: reduce heap allocations. The OnDraw hot path
  must not allocate per cell — keep `string(rune)` conversions and
  slice growth out of the inner loop if perf work begins.
