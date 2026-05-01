# go-term

A minimal terminal-emulator widget for the
[`go-gui`](https://github.com/mike-ward/go-gui) framework. Spawns a real
shell over a PTY and renders the cell grid through `gui.DrawCanvas`.

Targets macOS and Linux. Intentionally small — designed to stay
approachable, not feature-complete.

## Status

Pre-1.0. The public API in `term/` (`Cfg`, `Term`, `New`, `View`,
`Close`) is small on purpose and may still shift. See
[CHANGELOG.md](CHANGELOG.md).

## Features

- Real PTY-backed shell (`$SHELL`, fallback `/bin/sh`)
- 16-color ANSI palette (SGR 30–37, 40–47, 90–97, 100–107) plus
  bold / underline / inverse attributes
- Cursor movement, erase-in-line, erase-in-display
- UTF-8 input with carry-over across reads
- Keyboard input including arrows, Page Up/Down, Home/End, Delete,
  Ctrl+letter
- Live resize: floors the canvas to whole cells, reflows the grid,
  sends `TIOCSWINSZ` so the child receives `SIGWINCH`

## Requirements

- Go 1.26+
- macOS or Linux
- Sibling working trees of [`go-gui`](https://github.com/mike-ward/go-gui)
  and [`go-glyph`](https://github.com/mike-ward/go-glyph) at `../go-gui`
  and `../go-glyph` (referenced via `replace` directives in `go.mod`)

## Quickstart

```bash
git clone https://github.com/mike-ward/go-term.git
cd go-term/cmd/demo
go run .
```

Try `ls`, `cat`, ANSI color output, and resizing the window — then
`stty size` inside the embedded shell to confirm the child saw the
resize.

## Usage

```go
package main

import (
    "log"

    "github.com/mike-ward/go-gui/gui"
    "github.com/mike-ward/go-gui/gui/backend"
    "github.com/mike-ward/go-term/term"
)

func main() {
    var t *term.Term
    w := gui.NewWindow(gui.WindowCfg{
        Title:  "go-term",
        Width:  900,
        Height: 600,
        OnInit: func(w *gui.Window) {
            var err error
            t, err = term.New(w, term.Cfg{})
            if err != nil {
                log.Fatalf("term.New: %v", err)
            }
            w.UpdateView(t.View)
        },
    })
    defer func() {
        if t != nil {
            _ = t.Close()
        }
    }()
    backend.Run(w)
}
```

## Architecture

Three layers, one file each, dependencies flow strictly downward.

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

Concurrency: one PTY reader goroutine. `Grid.Mu` is the single lock —
the reader takes it to feed the parser; `OnDraw` takes it to read
cells. After feeding bytes, the reader calls `win.QueueCommand(...)` to
schedule the redraw on the main thread. Direct `*gui.Window` access
from the reader goroutine is forbidden.

## Out of scope

These were excluded from MVP and should remain so unless a feature
explicitly requires them. Each is a real chunk of work — please open
an issue first to discuss.

- Alt screen / xterm 1049 (vim, htop, less, tmux all need this)
- Mouse reporting (1000/1006)
- 24-bit truecolor
- Scrollback ring buffer
- Bracketed paste
- OSC sequences (titles, hyperlinks, OSC 52 clipboard)
- IME composition / dead keys
- Sixel / kitty graphics
- Windows / ConPTY support

## Testing

```bash
go test ./...
go test -race ./...
go vet ./...
```

The widget itself is GUI-only and cannot be validated headless — verify
visually by running `cmd/demo`.

## License

[MIT](LICENSE)
