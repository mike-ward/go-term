# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.11.0] - 2026-09-07

### Fixed

- `falcon.exe` no longer opens a console window behind the terminal. The binary
  is now linked `-H windowsgui`, which marks the PE as a GUI-subsystem image, so
  the Windows loader stops allocating a console for the process.

### Changed

- Release archives cover both architectures on every platform. amd64-only
  archives left arm64 Linux and Windows on ARM with nothing to download.
- The macOS download is one universal `.dmg`, fused with `lipo` from an arm64
  and an amd64 build. The previous build inherited the runner's architecture, so
  the `.dmg` would not run on an Intel Mac at all.
- Packaging moved into the Makefile, and the release workflow calls the same
  targets, so a release can be rehearsed with `make release` before the tag is
  pushed.
- CI no longer installs SDL2, FreeType, HarfBuzz, Pango or fontconfig, and no
  longer sets up an MSYS2 toolchain. The build is cgo-free on Linux and Windows:
  the ConPTY layer is pure-Go syscalls, go-glyph shapes in pure Go, and go-gui
  reaches GL through purego. None of those packages had a caller.

## [0.10.0] - 2026-09-02

### Added

- Cursor appearance is now a user setting. `[general] cursor-style` (`block`,
  `underline`, `bar`) and `cursor-blink` set the cursor a pane starts with and
  the one `reset` returns to; `cursor-lock` makes go-term ignore an
  application's `DECSCUSR`, pinning both. All three are also rows in the command
  palette, applied to every open pane for the session. `term.Cfg` gains
  `CursorStyle`, `CursorLocked`, and the live setters `SetCursorStyle`,
  `SetCursorBlink`, `SetCursorLocked`.
- Pane activity indicators: `term` now reports bells and OSC 133 command ends
  via `Cfg.OnActivity` / `ActivityKind`, and notifies command completion for
  background panes (`Cfg.NotifyAfter` / `SetNotifyAfter`) without synthesizing
  window focus events.
- `falcon`: soft heap limit to bound RSS after heavy sessions; `GOTERM_PPROF`
  profiling hook that keeps symbols when stripping.

### Changed

- **Breaking:** `term.Cfg.CursorBlink` is now a `bool` rather than a `*bool`,
  and seeds the cursor instead of overriding the application. The "override"
  half of the old pointer moved to the new `CursorLocked`, which covers shape
  and blink together. `nil` becomes `false`; `*true` becomes
  `CursorBlink: true, CursorLocked: true`.
- The internal `cursorShape` type is now the exported `term.CursorStyle`, so the
  configured value and the grid state are one type.
- `falcon` keeps config and workspace state under `~/.config/falcon` rather than
  `~/.config/go-term`.
- Blink, auto-scroll, and momentum tickers park when idle to reduce CPU/wakeups.
- Dependencies: go-gui v0.59.2 → v0.66.1, go-glyph v1.20.1 → v1.24.0
  (single-method `View`, per-scope IDs, and related API migrations).

### Fixed

- Bottom-margin scroll regions now correctly feed scrollback.
- Cmd-hover hyperlink highlight appears without requiring a mouse move.
- Mouse scroll coast velocity uses the latest sample instead of the first.
- Windows: ConPTY now measures text by grapheme cluster.

### Security

- URL-open injection fix; third-party GitHub Actions pinned by SHA with
  least-privilege release token.

### Build

- macOS app bundle signs via `SIGN_IDENTITY` (go-gui #303).
- `make prepush` local validation gate added.

## [0.9.0] - 2026-08-12

### API freeze

v0.9.0 fixes the public surface that v1.0.0 will inherit. Every exported symbol
now has a deliberate reason and a complete doc comment; what had no external
consumer is unexported. This is the release to build against — the intent is
that v1.0.0 (when go-gui reaches it) changes nothing here except removing the
remaining deprecation shims.

**Unexported in `term`:** `DefaultColor` (internal packed-color encoding).
`Fixture`/`CaptureFixture` stay public but are explicitly marked test
infrastructure with no compatibility guarantee.

**Unexported in `term/workspace`:** `Tab`, `SplitDir`/`SplitVertical`/
`SplitHorizontal`, and the keyboard-command methods an embedder never called —
`AddTab`, `CloseTab`, `ClosePane`, `SplitPane`, `NextPane`, `PrevPane`,
`GoToTab`, `MoveTabLeft/Right`, `NextTab`, `PrevTab`, `FocusPane`, `ToggleHelp`,
`TogglePalette`, `ToggleThemeBrowser`, `ToggleRecording`, `ToggleBroadcast`,
`Broadcasting`, `ReloadConfig`. The workspace keeps `Workspace`, `New`,
`Restore`, `Close`, `View`, `Cfg`, `Save`, `DefaultWorkspacePath`,
`DefaultConfigPath`, `LiveTermCount` and `ActivePane` (the last two because
falcon, the reference embedder, uses them).

**`RunAction` reaches every action.** It dispatches through a real
`Action → handler` table instead of synthesizing the action's chord, so an
action a user unbinds from the keyboard is still invocable from a command
palette. The copy-mode, search-bar and alt-screen mode gates move into the
dispatch path, byte-for-byte the same rules the keyboard applies; the copy-mode
key handlers now share one operation table with direct dispatch, so the two
cannot diverge.

### Added

- `term`: `GOTERM_LATENCY` keystroke-to-frame instrumentation. Set it to `1` (or
  to a sample-batch size) and every 25 keystrokes logs percentiles for the spans
  a keystroke passes through: `key→echo` (the child's round-trip), `echo→wake`
  (reader goroutine to the main thread picking the repaint up), `wake→paint`
  (the frame itself), and their total, alongside how long `onDraw` ran and how
  many PTY reads landed while the keystroke was outstanding — a shell echo is
  one, a full-screen TUI frame is many, and each one takes `grid.Mu`. Plus
  go-gui's own view-generation / layout / render-build split when the embedder
  enables `WindowCfg.Timings` (falcon does so under the same variable). Off by
  default and inert when off. It measures up to the end of `onDraw` only — GPU
  submit, compositor and vsync are invisible from inside the process and add
  another 8–17 ms on a 60 Hz display, so the numbers are a lower bound.
- `term`: `[env]` section in the config file plus `Cfg.Identity`: the host
  terminal's identity variables are scrubbed from each child's environment and
  `TERM_PROGRAM` names this terminal (`go-term`, or whatever the config says),
  so image-picking apps (yazi, superfile) choose a protocol go-term actually
  speaks.
- `term/workspace`: **Cmd+S saves the workspace.** New `workspace.save` command
  bound to Super+S (rebindable like every command); it writes the layout to the
  new `Cfg.SavePath` field, falling back to the default workspace path. Falcon
  sets `SavePath` to its effective save target, so Cmd+S and quit save to the
  same file.
- `falcon`: window icon and `WM_CLASS` on Linux/Windows; release workflow with
  packaging and a Homebrew tap.

### Changed

- **BREAKING: event callbacks take a single `gui.EventCtx`.** Bump go-gui
  v0.51.1 → v0.59.0. `(*gui.Layout, *gui.Event, *gui.Window)` becomes
  `func(gui.EventCtx)`, exposing the three as `ctx.Layout`, `ctx.Event` and
  `ctx.Window`; go-gui then migrated to the one-event rule (v0.55.0), removed
  `Padding` self-flags and `SomeP` (v0.57.0), and shipped per-scope IDs and
  debug categories (v0.56.0). `go-glyph` follows to v1.20.0. Migration guides
  for the go-gui side live in go-gui's `docs/`.
- **Consume-class callbacks are handled by default.** `OnClick` and `OnChar` are
  marked handled by dispatch before the callback runs. The help and palette
  backdrops gained an explicit `ctx.Bubble()` on their window-edge guard: that
  path exists so a resize drag starting near the window edge is not mistaken for
  a click-outside-to-dismiss, and it has to keep passing the press through
  rather than swallowing it.
- `term`: `Fixtures`-class recording bytes now stream through the same
  frame-boundary batching as glyph uploads — go-glyph v1.18.3 fixed a
  one-frame-lag that uploaded the whole atlas page per draw call, turning any
  glyph-cold frame (scrolling into scrollback rows the screen never painted)
  into GB-scale main-thread texture uploads (measured 355 ms per frame for 1500
  fresh glyphs; frame-boundary batching costs 17 ms / 43 MB).

### Fixed

- `term`: mouse-wheel scrolling was far too slow on Windows and Linux, and
  mismatched Ghostty on retina displays. A wheel delta is now measured in lines
  and scaled by the cell height (and the retina sensitivity matched), so one
  reported line moves exactly one grid row and a notch travels three — the xterm
  / kitty / Windows Terminal convention.
- `term`: Super+Shift+V paste no longer types a trailing `V` on Linux (the synth
  char event duplicated the paste).
- `term`: Windows pty teardown is bounded, so a live child cannot hang `Close`.
- `term`: the bottom-row background only bleeds when the row is uniform.
- `term`: cell origins snap to the device pixel grid, eliminating half-pixel
  seams on scaled displays.
- `docs`: `config.md` listed only the `Cmd` chord for every `workspace.*`
  command, so a Windows user reading the table saw shortcuts that cannot fire
  there — Super is OS-reserved and the defaults are remapped. Both forms are now
  listed, matching the `term.*` table.

## [0.7.0] - 2026-08-04

### Added

- `term`: SGR 53 / 55 overline. The rule is drawn in the text color, since SGR
  58 defines an underline color only. `attrOverline` sits below `attrProtected`,
  so SGR 0 clears it and the `attrVisual` blank-cell fast paths count an
  overlined space as non-blank.

- `docs`: "Known Omissions" section in `docs/terminal-verification.md`,
  recording that xterm's `modifyOtherKeys` is deliberately unimplemented because
  the Kitty Keyboard Protocol supersedes it — what it costs, and what evidence
  would reverse the decision. No behavior change; the sequences were already
  discarded, and now a test pins that they stay inert rather than leaking into
  SGR.

- `term`: `BundledThemes()` returns the 602 color themes go-term now ships — 473
  dark, 129 light — generated from the Ghostty-format schemes in
  [mbadolato/iTerm2-Color-Schemes](https://github.com/mbadolato/iTerm2-Color-Schemes)
  (MIT) into an embedded table and decoded on first use. Regenerate with
  `go run ./term/genthemes`; the full name list is in `docs/themes.md`.

- `workspace`: full-window theme browser on `Cmd+Shift+T`, replacing the old
  floating picker. It takes over the pane area instead of dimming it behind a
  scrim — the scrim made a theme impossible to judge while choosing it — and
  brings its own preview: the theme's 16 ANSI colors and a block of sample
  terminal output rendered on that theme's own background. Type to filter (a
  leading `dark:` or `light:` narrows by character), arrows and PageUp/PageDown
  move, `Enter` applies, `Escape` clears the filter and then cancels.

  The preview is deliberately more than a color chart, because a chart cannot
  answer the questions people actually have about a theme: the 16 ANSI swatches
  are labelled with the indices applications name them by, a short
  syntax-highlighted Go file exercises the keyword/string/number/type/comment
  slots an editor or `bat` reaches for (with one selected range, since the
  selection tint is derived and so cannot be guessed from the swatches), and a
  paragraph of prose plus a row of bold/italic/underline/dim/reverse shows
  whether the theme is readable for an hour rather than for a glance. It is all
  drawn in the pane's own font at the pane's own size.

- `term`: `Theme.SelectionBG()` returns the highlight background a theme gives
  ordinary selected text, for an embedder drawing chrome that has to match what
  the pane paints. Shares its blend with the per-cell draw path, so the two
  cannot round differently. Cancelling restores the theme that was active when
  the browser opened, which the old picker could not do: it applied on every
  arrow press and Escape simply stopped.

- `term`: Kitty Graphics Protocol Unicode placeholder placement — `U=1` (#118).
  An image sent that way creates a _virtual_ placement: it consumes no cells,
  blanks nothing, and does not move the cursor. It appears wherever the
  application later prints U+10EEEE placeholder cells, whose foreground color
  names the image, underline color the placement, and combining diacritics the
  tile. That is the only mode that survives being drawn inside a scrolling or
  layout-managed widget, because the terminal follows the cells rather than an
  absolute rectangle — yazi, tmux passthrough and the TUI image libraries all
  rely on it. Placeholder cells copy as blanks and never render their own
  character. Also honors `c=`/`r=` on ordinary placements: the image is scaled
  into the requested cell rectangle, and giving only one of the two derives the
  other from the aspect ratio.

- `term`: OSC 133 shell integration now uses the exit status shells report in
  `OSC 133;D;<exit>` (#103). `term.jump-failure` (`Cmd+Shift+E`) scrolls to the
  most recent command that exited non-zero — repeated presses walk back through
  older failures and wrap — and each failure gets a red tick in the scrollbar
  track so one buried in a long build log is findable by eye.
  `term.select-output` (`Cmd+Shift+O`) selects exactly the output region of the
  command under the cursor and enters copy mode with that selection live, ready
  for `y` or `Cmd+C`; on a fresh prompt it selects the previous command's
  output. Both are no-ops on the alt screen, and a command whose shell reported
  no exit status never counts as a failure. See `docs/config.md`.

- User config file covering fonts, theme, terminal settings and Term-level
  keybindings (#94). The existing INI at `~/.config/go-term/config` gains
  `[font]` (`family`, `size`) and `[general]` (`theme`, `scrollback`, `bell`,
  `scrollbar`) sections, and `[keybindings]` entries are now namespaced
  `term.<action>` or `workspace.<command>` — a bare key still means
  `workspace.`, so existing files keep working. `none` unbinds an action so the
  key reaches the child process. Collisions are detected across both namespaces,
  because go-gui's global commands outrank the widget's key handling and would
  otherwise shadow a `term.*` binding silently. `Cmd+Shift+,`
  (`Workspace.ReloadConfig`) re-reads the file and applies it to every live
  pane; a setting removed from the file reverts to the embedder's default. Parse
  errors are logged per line and never wedge the app. See `docs/config.md`.
- `term`: live setters for settings that `Cfg` previously fixed at construction
  — `SetTextStyle`, `SetScrollbackRows`, `SetBellMode`, `SetScrollbarWidth` —
  plus `ParseAction` for resolving an action name from a config file.
  `SetTextStyle` clears the runtime font zoom (an absolute size that would
  otherwise outrank the new configured one); `SetScrollbackRows` trims stored
  history immediately when shrinking rather than waiting for eviction.

- OSC 1337 `File=` downloads and real argument parsing (#75). The iTerm2
  sequence now carries file transfers (`inline=0`, as sent by `imgcat -d` and
  `it2dl`) in addition to inline images. Transfers are opt-in: embedders set
  `Cfg.OnDownload` to handle the bytes themselves, or `Cfg.DownloadDir` to use
  the built-in writer, which saves with `0600` permissions, suffixes name
  collisions (`report (1).pdf`) instead of overwriting, and reports the saved
  path through the existing notification path. Both unset — the default — leaves
  downloads disabled, so untrusted terminal output cannot create files. `falcon`
  opts in with `~/Downloads`; `workspace.Cfg.DownloadDir` passes the choice
  through.

  The `File=` key list is now parsed rather than substring-matched, so `width`
  and `height` (`N` cells, `Npx`, `N%`, `auto`) and `preserveAspectRatio`
  finally affect inline image size — `imgcat -W 40` renders at 40 columns
  instead of the image's natural size. `name` is base64-decoded and sanitized to
  a bare filename; path separators, traversal names, and control bytes cannot
  escape the download directory.

- Session recording and replay (#74). `falcon --record <file.gtr>` records the
  starting pane, `Cmd+Shift+R` toggles recording on the focused pane (marked by
  a `● REC m:ss` pill), and embedders get `Cfg.RecordPath`,
  `Term.StartRecording`, `StopRecording`, and `Recording`. Recordings capture
  pty output with timing plus grid resizes; keystrokes only when
  `Cfg.RecordInput` is set.

  `falcon --replay <file.gtr>` plays one back through a real `Term` — the
  parser, renderer, scrollback, selection, and search are the production ones,
  so a recording reproduces a rendering bug rather than describing it. Space
  pauses, `+`/`-` change speed, `.` steps a frame, `0` restarts.

  The `.gtr` container (`internal/recfmt`) is a JSON header line followed by
  `kind + delta-µs + length + raw bytes` frames. Payloads are never transcoded,
  so malformed UTF-8 — the byte sequences most worth reporting — survives the
  round trip; asciicast v2, whose events are JSON strings, cannot carry it and
  is therefore an export target rather than the storage format. Recording costs
  no allocations per frame and one write syscall.

  New `gotermrec` tool: `info`, `cat` (raw bytes — the `GOTERM_CAPTURE`
  workflow), `play` (timed playback in any terminal, no GUI), `fixture` (a
  replay fixture via `CaptureFixture`), and `export -cast` (asciicast v2, with a
  per-frame warning wherever bytes had to be replaced).

- DECSCA character protection and the VT420 rectangular area operations (#71):
  DECSCA (`CSI Ps " q`), the selective erases DECSEL (`CSI ? Ps K`), DECSED
  (`CSI ? Ps J`) and DECSERA (`CSI … $ {`) that honor it, plus DECERA (`$ z`),
  DECFRA (`$ x`), DECCARA (`$ r`), DECRARA (`$ t`), DECCRA (`$ v`) and the
  DECSACE extent selector (`CSI Ps * x`). DECRQSS answers `"q` and `*x`.
  Protection follows the DEC rule: only the selective erases skip a protected
  cell — ED/EL/ECH, scrolling and ordinary writes do not. DA1 still reports
  VT100 level, so applications that gate rectangle support on a VT420 device
  attributes reply will not use these.

- ECH (`CSI Ps X`, erase characters) — previously parsed and dropped. TUIs that
  paint split-pane layouts use it to clear a bounded span without `EL` wiping
  the panes sharing the row.
- CHT (`CSI Ps I`) and CBT (`CSI Ps Z`) tab navigation.
- `COLORTERM=truecolor` in the child environment (Unix and Windows). The widget
  renders 24-bit color, but `TERM=xterm-256color` alone only promises the
  palette, so TUI toolkits were quantizing truecolor output.

### Changed

- `term`: `deriveOverlay` now enforces the contrast floors the overlays are
  meant to keep, rather than reaching them by blend fractions that had been
  tuned by eye against a handful of dark themes. Measured against the full
  corpus, 36 of 602 themes produced chrome that washed out — a scrollbar thumb
  or a failure tick indistinguishable from the background — because no fixed
  percentage survives a mid-gray or saturated background. The visual-bell wash
  also derives its peak alpha per theme now: on a saturated mid-luma background
  (Hot Dog Stand, C64) even a pure white wash at the old alpha could not
  register. 5 of 602 themes need that; the rest are unchanged.

- `workspace`: theme selection is keyed by name rather than by `term.Theme`
  value. The bundled corpus contains distinct themes sharing an identical
  palette, so a value match could resolve to the wrong entry — putting the
  browser's checkmark on the wrong row and saving a theme name the user never
  chose.

- `workspace`: a pane's `term.Cfg.Themes` now carries only the selected theme.
  `term` reads element 0 and nothing else, so handing each pane a copy of the
  whole list cost a 602-entry copy per pane for one lookup.

- `term`: the configured font size (`Cfg.TextStyle.Size`, `SetTextStyle`) is now
  clamped to the same 4–72 pt bounds the zoom path already enforced, so no
  caller has to re-derive the limits.

- The OSC 1337 payload cap rose from 4 MiB to 32 MiB of base64 (~24 MiB of file
  data) to leave room for real downloads (#75). A payload that exceeds it is now
  dropped outright instead of silently truncated, and the enlarged accumulator
  is released after each sequence rather than pinned for the parser's lifetime.

### Removed

- `term`: the 16 predefined theme variables other than `DefaultTheme`
  (`GruvboxTheme`, `NordTheme`, `SolarizedDarkTheme`, `DraculaTheme`,
  `CatppuccinMochaTheme`, `TokyoNightTheme`, `MonokaiTheme`, `OneDarkTheme`,
  `RosePineTheme`, `KanagawaTheme`, `AyuDarkTheme`, `EverforestTheme`,
  `GitHubDarkTheme`, `SolarizedLightTheme`, `GitHubLightTheme`,
  `CatppuccinLatteTheme`). Every one is superseded by a corpus entry, and
  keeping both meant two sources of truth for the same theme name.
  `DefaultTheme` stays: it seeds the 256-color table's legacy index fallback and
  is what a grid uses when `Cfg.Themes` is empty.

  **Existing configs keep working.** The old _names_ still resolve —
  `theme = Tokyo Night` finds `TokyoNight`, `Solarized Dark` finds
  `iTerm2 Solarized Dark`, and so on — for both the config file and saved
  workspaces.

- `term`: `ThemeMenuItems`. go-term has no right-click menu; this was a helper
  for an embedder to feed `gui.ContextMenu` and had no callers, and its only
  natural argument now would build a 602-item menu.

- `workspace`: `CycleTheme`. No keybinding, no command, no callers, and "advance
  to the next of 602 themes" is not a usable operation. The theme browser
  replaces it.

### Fixed

- `term`: OSC 133 marks, the selection, and graphics origins no longer drift off
  their text when a window resize re-wraps content (#103). All three were
  shifted by the flat scrollback-depth delta, which is wrong as soon as a
  logical line re-wraps into a different number of physical rows — everything
  below it moved by the accumulated difference. They now re-map through the
  re-wrap itself, reusing the same logical-line mapping the cursor already used.
  Visible as prompt jumping landing on the wrong row after resizing a window
  with wrapped output in scrollback.

- Windows: the tail of a session's output is no longer truncated when the shell
  exits. ConPTY's output pipe is fed by conhost from its own process,
  asynchronously, so bytes the child had already produced could still be in
  flight when its process object signalled — and the child-exit path closed the
  read end at exactly that moment, discarding them. The last command's output
  vanished roughly half the time. The console and input pipe still go down on
  child exit, but the output read end now stays open so the reader drains to a
  natural EOF, with a bounded grace period as a backstop.
- IL (`CSI Ps L`) and DL (`CSI Ps M`) no longer move the cursor to column 0.
  xterm, wezterm and tmux all preserve the column; homing it made every
  following write on the row land at the wrong offset. Together with the missing
  ECH and CBT above, this left stale text across the screen in cell-diffing TUIs
  (charmbracelet/crush).
- A synchronized-update frame that completed mid-chunk is now painted
  immediately when the application has already opened the next block but has not
  written into it — the common `ESU BSU` boundary a pty read lands on.
  Previously that frame waited for the next read or the 500 ms watchdog. A block
  that has started writing still suppresses the repaint, so no half-drawn frame
  is shown.

## [0.6.0] - 2026-07-19

### Added

- Expand XTGETTCAP capability table to the full xterm-256color subset, improving
  capability queries for `tput`, `vim`, and other terminal programs (#53).

### Changed

- Hyperlink hover recolor and pointing-hand cursor are now gated on Cmd being
  held, matching the activation model (#54).
- Reuse SetGeom backing store and arena-carve reflow rows to reduce allocations
  during resize and scrolling (#55, #56) (#57).
- Bump go-glyph to v1.17.3 and go-gui to v0.40.0 (#58).

## [0.5.0] - 2026-07-17

### Added

- Mode 2026 synchronized-update watchdog with a 500 ms timeout that force-ends a
  block whose end never arrives (#50).
- Dedicated PTY resize goroutine (`resizeLoop`) for responsive resize that
  doesn't stall the reader (#49).
- `GOTERM_CAPTURE` debug tee that records each PTY's raw output to
  `<prefix>-<seq>.bin` for offline replay and debugging (#49).
- `lockMouse`/`unlockMouse` helpers on `Term` (#42).
- Multi-tick SGR mouse wheel reports with `ScrollPrecise`-based
  wheel-vs-trackpad detection (#37).
- **Windows support** via ConPTY backend (#19): `ptyIO` interface with split
  Unix/Windows PTY implementations (#17), platform-aware shortcut modifiers
  (#20), and toast notifications (#23).
- `ExitWhenLastShellExits` workspace option (#14).
- `Cmd+=`/`Cmd+-` keyboard shortcuts to adjust font size by 0.25 pt (#13).
- Tab reordering via `Cmd+Alt+[` / `Cmd+Alt+]` (#12).

### Fixed

- Cancel drag on window resize; guard the help-dialog backdrop from edge clicks
  (#46).
- Mouse-selection off-by-one when the canvas is vertically offset by a tab bar
  (#34).
- `posToCell` row mapping when smooth-scrolled (ViewSubPx) (#29).
- Clear scrollback on CSI 3 J (#30).
- Mouse reporting-drag coordinate offset when canvas is offset (#42).
- Fall back to `$HOME` when the saved CWD directory no longer exists.
- Brahmic akshara cell width: virama fusion, Mc marks, and dangling virama are
  now sized correctly.
- Benchmark regression gate: `ns/op` is advisory-only; the hard gate checks only
  `allocs/B-op` (#7).

### Changed

- Help dialog: headings and key labels use the default text color; sections
  separated by thin dividers (#45).
- Inactive tab title text is dimmed to distinguish active from inactive tabs
  (#35).
- Scrollbar now has hover brightness, click/drag, and an edge inset (#33); the
  thumb is clamped to a minimum pixel height (#31).
- Mouse-wheel scroll sensitivity reduced from 15 to 5 rows (#32).
- Scroll momentum decay shortened (#36).
- Selection boundaries use half-open intervals (#30).
- Renamed `examples/demo` to `examples/falcon` (#16); consolidated the
  font-family constant (#26).
- Compressed ROADMAP from 606 to 135 lines.

## [0.4.0] - 2026-06-28

### Added

- Fuzz testing for parser input on PRs that touch parser files.
- Benchmark regression gates with a zero-allocation hard gate for the
  foreground-pass hot path.
- Conformance smoke tests for vttest-parity VT/xterm edge cases.
- Whole-app replay fixtures covering tmux, paste, graphics, BiDi, and mouse.
- `script2fixture` tool for capturing replay fixtures from `script(1)`
  typescripts.
- Emoji fill their reserved cell box at any DPI via go-glyph's `EmojiBoxWidth`
  hint (requires go-gui v0.29.0 / go-glyph v1.12.0).

### Fixed

- Grapheme clusters split across a PTY read boundary (e.g. a ZWJ emoji at the
  4096-byte edge) are no longer committed as broken pieces; the trailing,
  still-growing cluster is carried to the next read and flushed when the input
  burst drains.

## [0.2.0-rc.1] - 2026-05-30

### Added

- **256-color and 24-bit truecolor** (`CSI 38;2;r;g;b m` / `CSI 38;5;n m`).
- **Scrollback ring buffer** with mouse wheel, PgUp/PgDn, and pixel-perfect
  sub-row scrolling with two-phase momentum deceleration.
- **Text selection** with content-relative coordinates that survive scroll and
  resize; clipboard copy/paste (`Cmd+C`/`Cmd+V`); OSC 52 clipboard write
  (opt-in).
- **Alt screen** (DECSET 47 / 1047 / 1049); scrollback suppressed while active.
- **Scroll regions** (DECSTBM), IL/DL/ICH/DCH, and IND/RI.
- **OSC protocol**: window title (0/1/2), CWD (7), hyperlinks (8; Cmd+click
  opens browser), desktop notifications (9/777), dynamic colors (10/11/12),
  clipboard (52), semantic shell marks (133), iTerm2 inline images (1337).
- **Mouse reporting**: X10 (`?1000`), button-event (`?1002`), any-motion
  (`?1003`), SGR encoding (`?1006`), SGR-Pixels mode (`?1016`).
- **Cursor styles** (DECSCUSR): block, underline, bar; steady or blinking;
  `Cfg.CursorBlink` override.
- **Extended SGR**: italic, dim, strikethrough, extended underlines
  (double/curly/dotted/dashed with per-cell color via `CSI 58`).
- **East Asian Wide characters** and ZWJ combining marks via `uniseg`.
- **Logical line reflow** on window resize.
- **Kitty Keyboard Protocol** (`CSI u`) with key-release events and left/right
  modifier distinction.
- **Search in scrollback**: `Cmd+F` literal, `Ctrl+R` regex; match highlighting;
  `Enter`/`Shift+Enter` cycling.
- **Color themes**: `Theme` struct with 16 ANSI colors + default fg/bg; runtime
  switching; bundled Gruvbox, Nord, and Solarized Dark.
- **Sixel graphics**: 256-register color, RLE, up to 4096×4096 px.
- **Kitty Graphics Protocol**: chunked base64 transmission; PNG / raw RGBA / raw
  RGB; off-screen store.
- **Bidirectional text** (Unicode UAX#9) for RTL languages.
- **Scrollbar indicator** with auto-hide and fade.
- **Visual bell** flash on `BEL`.
- **Dirty-row tracking** and tessellation cache for low-CPU idle frames.
- **Semantic shell marks** (OSC 133): `Cmd+Up/Down` jumps between command
  boundaries.
- **Synchronized Updates** (DCS `?2026`).
- **DECRQSS** and **XTGETTCAP** reply dispatch.
- Test suite covering grid, parser, palette, widget, and PTY helpers.
- `MaxGridDim` and `MaxScrollbackCap` constants; dimension and param bounds
  against pathological input.

### Fixed

- Cursor disappearing at the right margin when `CursorC == Cols`.
- `EraseInLine` and `EraseInDisplay` now propagate current attributes.
- `Tab` no longer divides a negative `CursorC`.
- Truecolor and 256-color SGR parsing bounds-checked to parameter count.
- PTY writes log errors instead of silently dropping them.

### Changed

- `encodeRune` replaced by standard library `utf8.EncodeRune`.
- `Grid.Resize` and `NewGrid` clamp inputs through `clampDim`.

## [0.1.0] - 2026-05-01

### Added

- Initial public release.
- `term.Term` widget bound to a single PTY-backed shell.
- VT parser supporting C0 control bytes, CSI cursor moves, erase-in-line,
  erase-in-display, and SGR for the ANSI 16-color palette plus bold / underline
  / inverse.
- 16-color palette (VS Code Dark+ approximation) with default fg/bg.
- `examples/falcon` example window.
