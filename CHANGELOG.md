# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Test suite covering grid, parser, palette, and PTY helpers
  (`term/grid_test.go`, `term/parser_test.go`, `term/widget_test.go`,
  `term/palette_test.go`, `term/pty_test.go`).
- `MaxGridDim` constant and `clampDim` helper bounding grid dimensions
  to prevent runaway allocation.
- `clampWinsize` helper bounding rows/cols passed to the kernel ioctl.
- `runeString` ASCII string cache to keep `OnDraw` allocation-free for
  the common case.
- `finite` float check guarding `OnDraw` against NaN/Inf canvas or cell
  metrics.
- `maxCSIParams` and `maxCSIParamValue` caps in the VT parser to bound
  memory use against pathological streams.

### Fixed

- Cursor disappearing at the right margin when `CursorC == Cols`.
- `EraseInLine` and `EraseInDisplay` now propagate the current
  attributes (`AttrUnderline`, etc.) into blanked cells.
- `Tab` no longer divides a negative `CursorC`.
- `pty.Resize` and `onChar` now log write/resize errors instead of
  silently dropping them.
- Truecolor (`SGR 38;2;...`) and 256-color (`SGR 38;5;...`) swallow
  logic now bounds the skip to the actual parameter list length.

### Changed

- `encodeRune` helper removed in favor of the standard library
  `utf8.EncodeRune`.
- `Grid.Resize` and `NewGrid` now clamp inputs through `clampDim`
  rather than only enforcing a lower bound.

## [0.1.0] - 2026-05-01

### Added

- Initial public release.
- `term.Term` widget bound to a single PTY-backed shell.
- VT parser supporting C0 control bytes, CSI cursor moves,
  erase-in-line, erase-in-display, and SGR for the ANSI 16-color
  palette plus bold / underline / inverse.
- 16-color palette (VS Code Dark+ approximation) with default fg/bg.
- `cmd/demo` example window.
