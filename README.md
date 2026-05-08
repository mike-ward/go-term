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
- Full color support: 16-color ANSI, 256-color palette, and 24-bit Truecolor
- Advanced text attributes: Bold, Dim, Italic, Underline, Inverse, Strikethrough
- Logical line wrapping (reflow): Content is re-wrapped on window resize
- Scrollback ring buffer with momentum scrolling (default 5000 rows)
- Alt-screen support for full-screen apps (`vim`, `htop`, `less`, `tmux`)
- Mouse reporting (SGR 1006) and mouse wheel support
- Text selection and system clipboard integration (Copy/Paste)
- OSC support: Window titles, Hyperlinks (OSC 8), CWD, and Clipboard (OSC 52)
- Bracketed paste mode and Focus reporting
- Search in scrollback (Cmd+F)
- East Asian Wide (CJK) and Emoji support

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
term/palette.go        256-color ANSI table + RGB resolution.
```

Concurrency: one PTY reader goroutine. `Grid.Mu` is the single lock —
the reader takes it to feed the parser; `OnDraw` takes it to read
cells. After feeding bytes, the reader calls `win.QueueCommand(...)` to
schedule the redraw on the main thread. Direct `*gui.Window` access
from the reader goroutine is forbidden.

## Out of scope (still)

These are currently excluded from the roadmap. Each is a real chunk of work:

- IME composition / dead keys
- GPU-accelerated rendering
- Windows / ConPTY support

## Testing

```bash
go test ./...
go test -race ./...
go vet ./...
```

For emulator behavior beyond unit tests, this repo now uses a replay
suite that feeds realistic byte streams into the parser and asserts the
final screen state, cursor position, OSC side effects, and host replies.
See [docs/terminal-verification.md](docs/terminal-verification.md).

The widget itself is still GUI-bound, so final verification should also
include `cmd/demo` for resize, redraw, selection, paste, and application
compatibility checks.

## License

[MIT](LICENSE)
