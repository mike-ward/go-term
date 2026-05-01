# Contributing

Thanks for your interest in `go-term`. This is a small, deliberately
narrow project — please read this file before opening a PR.

## Development setup

`go-term` depends on sibling working trees, wired up via `replace`
directives in `go.mod`:

```
replace (
    github.com/mike-ward/go-glyph => ../go-glyph
    github.com/mike-ward/go-gui   => ../go-gui
)
```

Clone all three repos as siblings:

```bash
git clone https://github.com/mike-ward/go-glyph.git
git clone https://github.com/mike-ward/go-gui.git
git clone https://github.com/mike-ward/go-term.git
```

Edits in `../go-glyph` and `../go-gui` are picked up immediately by
`go build` from `go-term`.

## Toolchain

- Go 1.26+
- macOS or Linux

## Common commands

```bash
# Run the demo window
cd cmd/demo && go run .

# Build everything
go build ./...

# Tests (pure-logic only — widget is GUI and verified visually)
go test ./...
go test -race ./...

# Vet
go vet ./...

# Tidy module graph
go mod tidy
```

## Scope

Before adding a feature, check the **Out of scope** list in
[README.md](README.md). Items there were excluded deliberately. If you
want one of them, open an issue first to discuss whether the cost is
worth carrying — the goal is to keep the codebase approachable.

The public API in `term/` (`Cfg`, `Term`, `New`, `View`, `Close`) is
small on purpose. Add unexported helpers freely; expand the public
surface only when there is a clear caller need.

## Architectural rules

- Dependencies flow strictly downward: `widget → parser → grid`. The
  parser must not reach into go-gui — it is grid-only.
- `Grid.Mu` is the single lock. The reader goroutine takes it to feed
  the parser; `OnDraw` takes it to read cells. Never hold it across a
  go-gui call from the reader goroutine.
- `*gui.Window` state is touched only on the main thread.
  `win.QueueCommand(...)` is the only thread-safe path from the reader
  goroutine.
- `OnDraw` runs every frame. Avoid per-cell heap allocations in the
  inner loops — use the existing patterns (e.g. `runeString` for
  ASCII) rather than `string(rune)`.

## Code style

- Comments wrap at ~90 columns when practical.
- Error handling: log and continue at boundaries that aren't fatal
  (e.g. `pty.Resize`); return errors from constructors.
- Modern Go (1.26+) idioms — `for i := range n`, `slices`, `maps`,
  `cmp.Or`, `errors.Is`/`As`, `t.Context()` in tests, etc.
- Bound user-supplied counts and sizes. `clampDim` and `clampWinsize`
  exist for this reason; reuse them.

## Pull request checklist

1. `go build ./...` passes
2. `go vet ./...` passes
3. `go test -race ./...` passes
4. New tests for new pure-logic code
5. Manual smoke test of `cmd/demo` for any change touching
   `widget.go`, `pty.go`, or render/input paths
6. CHANGELOG entry under `## [Unreleased]`

## Reporting bugs

Include:
- OS and version
- Go version (`go version`)
- Shell (`echo $SHELL`)
- Minimal repro: what was typed, what was expected, what happened

## License

By contributing, you agree your contributions are licensed under
[MIT](LICENSE), the same license as the project.
