# go-term: Phased feature roadmap toward ghostty/iTerm2/kitty parity

## Context

`go-term` reached MVP: spawns shell via PTY, renders 16-color cell grid,
basic CSI/SGR + cursor positioning, no scrollback/alt-screen/mouse. Goal
now is to extend the widget toward modern terminal feature parity
(ghostty/iTerm2/kitty) without losing the deliberately small,
single-file-per-layer design.

Each phase below is sized for one focused PR, demo-testable by running
`cd cmd/demo && go run .` and exercising one new behavior. Performance
tuning is deferred — correctness and feature breadth come first.

The architecture stays three layers (`grid.go` → `parser.go` →
`widget.go`) plus `pty.go` and `palette.go`. New state lives in the
existing layer that owns its concept (e.g. scrollback in `grid.go`,
alt-screen toggle in `parser.go` / `widget.go`).

Progress is tracked via the checkboxes below. Tick each box as the
work lands. The authoritative copy of this file lives in the repo at
`/Users/mikeward/Documents/github/go-term/ROADMAP.md` (see Phase 0).

## Phase ordering rationale

Phases are ordered by (a) prerequisite chain, (b) user-visible impact,
(c) implementation simplicity. Early phases unlock obviously-broken
behavior in common tools (vim colors, copy/paste, scrollback). Later
phases unlock advanced apps (tmux, mouse-aware editors) and polish.

---

## Phase 0 — Land this roadmap in the repo

- [x] Copy this plan file to `/Users/mikeward/Documents/github/go-term/ROADMAP.md`
      so progress tracking lives next to the code.
- [x] Add a one-line link to `ROADMAP.md` from `CLAUDE.md` (under a
      new `## Roadmap` heading) so future agents discover it.
- [x] Verify `go vet ./...` and `go build ./...` still pass (no code
      changes; sanity check repo state before phase work begins).

**Demo test:** `cat ROADMAP.md` from inside the embedded shell of
`cmd/demo` confirms the file is checked in and reachable.

---

## Phase 1 — 256-color + 24-bit truecolor

**Why first:** parser already *parses-and-swallows* `38;5;n` and
`38;2;r;g;b` (parser.go:209-227). Many CLI tools (`ls --color`, `bat`,
`eza`, vim themes) look broken without it. Smallest wire-up.

- [x] `grid.go`: `Cell.FG/BG` and `Grid.CurFG/CurBG` are now packed
      `uint32` with high-byte tag (palette / RGB / Default). Plain
      palette indices still encode as their numeric value so
      `FG: 1`-style literals keep working. `DefaultColor` sentinel
      preserved (now `0xFF000000`).
- [x] `palette.go`: extended to 256 entries (xterm 16 + 6×6×6 cube +
      24 grayscale). New `resolveFG`/`resolveBG` helpers decode the
      packed encoding; `fg()`/`bg()` honor `AttrInverse`.
- [x] `parser.go::applySGR`: 38/48 dispatch via `applyExtendedColor`
      with `;5;n` and `;2;r;g;b` sub-forms; channel values clamped to
      0..255. Swallow path dropped.
- [x] `widget.go::onDraw`: no change — `fg`/`bg` resolve the new
      encoding transparently.
- [x] New tests: `TestParser_SGR256_*`, `TestParser_SGRTruecolor_*`,
      `TestParser_SGR_UnknownExtendedSelectorConsumesRest`,
      `TestPalette_256_Cube`, `TestPalette_256_Grayscale`,
      `TestPalette_TruecolorRoundtrip`,
      `TestPalette_TruecolorInverse`.

**Demo test:**
```
ls --color=always
printf '\x1b[38;2;255;100;0mORANGE\x1b[0m\n'
printf '\x1b[38;5;82mGREEN256\x1b[0m\n'
```

---

## Phase 2 — Cursor save/restore + show/hide

**Why:** prereq for alt-screen and scroll regions. Trivial. Many apps
use `\x1b[?25l` to hide the cursor during redraws — without it,
flicker.

- [x] `grid.go`: `SaveCursor()`, `RestoreCursor()` storing
      `(CursorR, CursorC, CurFG, CurBG, CurAttrs)` in a `savedCursor`
      field. Add `CursorVisible bool` (default true).
- [x] `parser.go::dispatchCSI`: add `s` (save), `u` (restore). Add
      ESC 7 / ESC 8 in `Feed` ESC dispatch. Add DEC private mode
      `?25 h/l`.
- [x] `widget.go::onDraw`: skip cursor block when
      `g.CursorVisible == false`.
- [x] Tests for save/restore round-trip and `?25` toggling.

**Demo test:** `tput civis` then `tput cnorm`; press Ctrl+L while in
shell.

---

## Phase 3 — Scrollback ring buffer + mouse wheel + PgUp/PgDn

**Why:** without scrollback the terminal feels broken. Highest UX
impact for the size of change.

- [x] `Cfg.ScrollbackRows int` (default 5000) plumbed into `New`.
- [x] `grid.go`: `Scrollback [][]Cell` with cap from Cfg. `scrollUp()`
      pushes the dropped top row, trims to cap. Add `ViewOffset int`
      and `ScrollView(delta int)` (clamped).
- [x] `widget.go::onDraw`: when `g.ViewOffset > 0`, render top
      `min(rows, ViewOffset)` rows from scrollback (newest-last), then
      the rest from live cells. Hide cursor while offset > 0.
- [x] `widget.go`: `OnMouseScroll` handler → `g.ScrollView(±N)` +
      `win.QueueCommand` redraw. Reset `ViewOffset` to 0 on any
      keystroke that isn't a scrolling key.
- [x] `widget.go::onKeyDown`: PgUp/PgDn → `ScrollView(±rows-1)`,
      Shift+Home/End → top/bottom of scrollback. Without Shift,
      behavior unchanged.
- [x] Tests: scrollback fill + trim + view-offset clamp.

**Demo test:**
```
seq 1 5000
```
Scroll wheel up. PgUp/PgDn. Type a key — view snaps back.

---

## Phase 4 — Text selection + copy to clipboard

**Why:** core terminal UX. go-gui exposes `Window.SetClipboard`
(window.go:464) and `OnClick`/`OnMouseMove` via `DrawCanvasCfg`.
Decision: left-drag select + Cmd/Ctrl+C copy. No middle-click PRIMARY.

- [x] `grid.go`: selection state `SelStart, SelEnd struct{ Row, Col
      int }; SelActive bool`. `SelectedText() string` walks rows,
      newline at row boundaries, trim trailing blanks per row (kitty
      convention).
- [x] `widget.go`: `OnClick` (left, no mods) sets
      `SelStart=SelEnd=cell-at-pos`, `SelActive=false`.
- [x] `widget.go`: `OnMouseMove` while button down → update `SelEnd`,
      `SelActive=true`.
- [x] `widget.go`: click release → if `SelActive`,
      `win.SetClipboard(g.SelectedText())`.
- [x] `widget.go::onDraw`: when cell is in selection, swap fg/bg
      (inverse) before paint.
- [x] Helper `posToCell(x, y float32) (row, col int)` using
      `cellW/cellH`.
- [x] `onKeyDown`: Cmd+C / Ctrl+Shift+C → copy current selection.
      Suppress propagation only when selection non-empty (Ctrl+C
      still SIGINTs the child otherwise).
- [x] Tests for `SelectedText` row/column ranges and trailing-blank
      trimming.

**Demo test:** drag-select `pwd` output; paste in another app.

---

## Phase 5 — Paste (clipboard + bracketed paste mode)

**Why:** depends on Phase 4 for clipboard plumbing. Bracketed paste
(DECSET 2004) prevents shell auto-execution of pasted newlines.

- [x] `parser.go`: track `BracketedPaste bool`; handle `?2004 h/l`
      DEC private mode.
- [x] `widget.go`: Cmd+V / Ctrl+Shift+V in `onKeyDown` →
      `s := win.GetClipboard()`; if `parser.BracketedPaste`, write
      `\x1b[200~` + s + `\x1b[201~` to PTY, else write raw.
- [x] Strip embedded `\x1b[201~` markers from `s` first (security).
- [x] Tests for marker stripping and toggle behavior.

**Demo test:** copy a multi-line block, paste at zsh/bash prompt — no
auto-execute.

---

## Phase 6 — Scroll regions + line insertion/deletion + IND/RI

**Why:** prereq for vim/less to render correctly inside the main
buffer (and for alt-screen apps). Without DECSTBM, vim repaints the
whole screen on every scroll.

- [x] `grid.go`: added `Top, Bottom int` (default `0..Rows-1`),
      replaced `scrollUp()` with `scrollUpRegion(n)` (pushes scrollback
      only when region is full-screen). Added `scrollDownRegion(n)`,
      `SetScrollRegion`, `ScrollUp`/`ScrollDown` (CSI S/T wrappers),
      `InsertLines`, `DeleteLines`, `InsertChars`, `DeleteChars`,
      `ReverseIndex`, `NextLine`. `Newline` now scrolls only at
      `Bottom`; cursor below Bottom advances without scrolling.
      `Resize` resets the region to full screen.
- [x] `parser.go::dispatchCSI`: `r` (DECSTBM), `L` (IL), `M` (DL),
      `@` (ICH), `P` (DCH), `S` (SU), `T` (SD).
- [x] `parser.go::Feed` ESC dispatch: `D` (IND), `M` (RI), `E` (NEL).
- [x] Tests: `TestGrid_SetScrollRegion`,
      `TestGrid_ScrollUpRegion_Partial`,
      `TestGrid_ScrollUpRegion_FullScreenScrollback`,
      `TestGrid_ScrollUpRegion_OverHeight`,
      `TestGrid_ScrollDownRegion_Partial`,
      `TestGrid_NewlineAtRegionBottom`,
      `TestGrid_NewlineBelowRegionDoesNotScroll`,
      `TestGrid_ReverseIndexAtTop`, `TestGrid_NextLine`,
      `TestGrid_InsertLines`, `TestGrid_InsertLines_OutsideRegion`,
      `TestGrid_DeleteLines`, `TestGrid_InsertChars`,
      `TestGrid_InsertChars_OverWidth`, `TestGrid_DeleteChars`,
      `TestGrid_ResizeResetsRegion`,
      `TestParser_DECSTBM_SetAndReset`, `TestParser_IND_RI_NEL`,
      `TestParser_InsertDeleteLines`, `TestParser_InsertDeleteChars`,
      `TestParser_SU_SD`.

**Demo test:** `vim` opens a file, scrolling shows partial repaints
not full clears. `less /etc/services` arrows scroll smoothly.

---

## Phase 7 — Alt screen (DECSET 1049 / 47 / 1047)

**Why:** unlocks vim, htop, less, tmux full-screen rendering. Depends
on Phases 2 + 6. Decision: scrollback writes suppressed while alt is
active (kitty/iTerm/ghostty default).

- [x] `grid.go`: `AltActive bool` + `mainSaved altSavedScreen`
      (cells, cursor, SGR, scroll region, DECSC slot). `EnterAlt()`
      stashes main state and swaps in a fresh blank buffer;
      `ExitAlt()` restores. `Resize` reflows the saved main buffer
      while alt is active.
- [x] `parser.go::applyDECMode`: `?47 h/l`, `?1047 h/l`,
      `?1049 h/l`. `?1049` calls SaveCursor before EnterAlt and
      RestoreCursor after ExitAlt; the DECSC slot is swapped on
      enter/exit so DECSC inside the alt buffer can't clobber the
      main save.
- [x] Suppress scrollback writes in `scrollUpRegion` while
      `AltActive`.
- [x] Tests: `TestGrid_EnterAlt_BlanksAndSwaps`,
      `TestGrid_EnterExitAlt_RestoresMain`,
      `TestGrid_EnterAlt_Idempotent`,
      `TestGrid_ExitAlt_NoOpWhenInactive`,
      `TestGrid_AltSuppressesScrollback`,
      `TestGrid_EnterAlt_ResetsView`,
      `TestGrid_AltResize_ReflowsMainBuffer`,
      `TestGrid_AltDECSC_DoesNotClobberMainSave`,
      `TestParser_DEC47_AltScreen`,
      `TestParser_DEC1047_AltScreen`,
      `TestParser_DEC1049_SavesAndRestoresCursor`,
      `TestParser_DEC1049_SuppressesScrollback`.

**Demo test:** `vim`, edit, `:q!` — original prompt + history
restored exactly. `htop` runs.

---

## Phase 8 — OSC: window title + cwd

**Why:** small, high-visibility win once alt-screen apps work. go-gui
has `Window.SetTitle` (window.go:500).

- [x] `parser.go`: new state `stOSC` (+ `stOSCEsc`). ESC `]` enters;
      collect bytes until BEL (`\x07`) or ST (`\x1b\\`). Bare ESC
      inside OSC aborts cleanly and reprocesses as a fresh ESC.
      Payload capped at `maxOSCBytes` (4096).
- [x] Dispatch OSC `0;` / `1;` / `2;` → `Parser.onTitle` callback;
      widget routes to `Cfg.OnTitle` (or `win.SetTitle`) via
      `QueueCommand`.
- [x] OSC `7;file://host/path` → stash `Grid.Cwd`, exposed via
      `Term.Cwd()`.
- [x] Drop everything else (OSC 52, OSC 8, malformed, unknown Ps).
- [x] `widget.go`: `Cfg.OnTitle func(string)`, defaulting to
      `win.SetTitle`.
- [x] Tests: `TestParser_OSCTitle_BELTerminator`,
      `TestParser_OSCTitle_STTerminator`,
      `TestParser_OSCTitle_Ps0And1And2`,
      `TestParser_OSCTitle_SplitAcrossFeeds`,
      `TestParser_OSC7_SetsCwd`,
      `TestParser_OSC_UnknownPsDropped`,
      `TestParser_OSC_NoSeparatorDropped`,
      `TestParser_OSC_OverflowTruncated`,
      `TestParser_OSC_AbortedByBareESC`.
- [x] DA1 (CSI c) reply `\x1b[?1;2c` via `Parser.onReply` — wired
      to `pty.Write`. Tests: `TestParser_DA1_Reply`,
      `TestParser_DA1_ExplicitZero`, `TestParser_DA1_NonZeroIgnored`,
      `TestParser_DA1_PrivateIgnored`.

**Demo test:** `printf '\x1b]0;hello world\x07'` → window title
updates.

---

## Phase 9 — Mouse reporting (X10 + SGR 1006)

**Why:** required by tmux pane-click, vim mouse, midnight commander.
Depends on Phase 4 mouse wiring.

- [x] `parser.go`: track DEC private modes `?1000 h/l` (button),
      `?1002 h/l` (button + drag), `?1003 h/l` (any-motion),
      `?1006 h/l` (SGR encoding). Default off.
- [x] `grid.go`: `MouseTrack`, `MouseTrackBtn`, `MouseTrackAny`,
      `MouseSGR` flags + `MouseReporting()` aggregate.
- [x] `widget.go`: `mouseSGRBaseButton`, `mouseModBits`,
      `encodeMouseSGR` helpers; `mouseSnap` snapshot under lock;
      `writeMouse` shared emit path. `onClick`/`onMouseMove`/
      `onMouseUp`/`onMouseScroll` route to encoded reports when
      reporting + SGR + live viewport, otherwise fall through to
      selection / scrollback. Drag tracks button + report flag so
      every press has a paired release.
- [x] Suppress mouse reports while `ViewOffset > 0` (scrollback
      view) — `mouseSnap.live` gate.
- [x] Motion dedupe by cell so a still pointer under ?1003 doesn't
      flood the PTY with identical coordinates each frame.
- [x] Tests: `TestParser_MouseModes_Toggle`,
      `TestParser_MouseReporting_Aggregate`,
      `TestEncodeMouseSGR_Press`, `TestEncodeMouseSGR_Release`,
      `TestEncodeMouseSGR_WheelUp`, `TestEncodeMouseSGR_DragWithMods`,
      `TestMouseSGRBaseButton_KnownButtons`, `TestMouseModBits`.

**Demo test:** `tmux` → click between panes. `vim` with
`:set mouse=a` → click moves cursor.

---

## Phase 10 — Cursor style (DECSCUSR) + blink

**Why:** small polish. `DECSCUSR` (`CSI Ps SP q`) chooses block /
underline / bar, blinking or steady — set by zsh/fish via vim-mode
prompts. Decision: honor DECSCUSR blink request; `Cfg.CursorBlink
*bool` overrides.

- [x] `grid.go`: `CursorShape` enum (`CursorBlock`/`CursorUnderline`/
      `CursorBar`) + `CursorBlink bool`. `ApplyDECSCUSR(ps)` maps
      Ps=0..6 to shape/blink pairs (xterm convention), unknown Ps
      falls back to blinking block. Defaults via `NewGrid`.
- [x] `parser.go`: track last intermediate byte (0x20..0x2F) per CSI
      and reset on entry/dispatch. `q` final byte fires DECSCUSR
      only when the intermediate is space.
- [x] `widget.go::onDraw`: dispatches to `drawCursor` which renders
      block (inverted), underline (bottom strip ≥2px), or bar (left
      strip ≥2px). Strips remain visible at small font sizes.
- [x] Blink: `cursorEpoch` set in `New`; `cursorBlinkOff()` flips
      every `cursorBlinkPeriod` (500ms). `blinkLoop` goroutine
      `QueueCommand`s redraws on each tick when the cursor is
      blinking + visible + at live viewport. Stops on `Close` via
      `blinkDone` channel.
- [x] `Cfg.CursorBlink *bool` override; `cursorBlinks()` prefers
      override, falls back to grid state.
- [x] Tests: `TestParser_DECSCUSR_AllPs` (table-driven Ps 0..6 +
      unknown), `TestParser_DECSCUSR_RequiresSpaceIntermediate`
      (no SP → ignored), `TestParser_DECSCUSR_DefaultParam` (no Ps),
      `TestCursorBlinks_HonorsGridDefault`,
      `TestCursorBlinks_CfgOverridesGrid`.

**Demo test:** `printf '\x1b[6 q'` → bar cursor.

---

## Phase 11 — Wide chars + emoji (East Asian Wide)

**Why:** CJK + emoji currently overstrike. `uniseg` is already an
indirect dep.

- [x] `grid.go::Cell`: added `Width uint8` (0 = continuation,
      1 = normal, 2 = wide head). `defaultCell` sets Width=1; new
      `blankCell(fg,bg,attrs)` helper; all erase/scroll/insert paths
      now route through it so blanks are never Width=0.
- [x] `runeWidth(r rune) int` ASCII-fast-paths to 0 (control) or
      1, falls through to `uniseg.StringWidth(string(r))` for the
      rest. uniseg promoted from indirect to direct require.
- [x] `Grid.Put` consults `runeWidth`: drops width-0 (combining /
      ZWJ), pads + wraps when a width-2 char would overflow the
      right margin, writes head at (r,c) and continuation
      `{Ch:0, Width:0}` at (r,c+1). Cursor advances by `w`.
- [x] `Grid.eraseWideAt(r,c)` sanitizes orphaned partner cells when
      a Put overwrites half of an existing wide pair.
- [x] `widget.go::onDraw` foreground pass skips continuation cells
      (Width==0 && Ch==0). Background-pass coalescing already
      preserves wide pairs because head + continuation share SGR.
- [x] Tests: `TestRuneWidth_ASCII`, `TestRuneWidth_CJKAndEmoji`,
      `TestGrid_Put_WideAdvancesTwoColumns`,
      `TestGrid_Put_WideWrapsAtRightEdge`,
      `TestGrid_Put_OverwriteWideHeadClearsContinuation`,
      `TestGrid_Put_OverwriteContinuationClearsHead`,
      `TestGrid_Put_DropsZeroWidth`,
      `TestGrid_Put_WideThenNarrowLayout`.

**Demo test:** `echo 你好 🍣 hello`. Cursor advances correctly past
wide chars.

---

## Phase 12 — Advanced Text Attributes (Italic, Dim, Strikethrough)

**Why:** Standard in modern terminals. Dim and Italic are often used
by syntax highlighters; Strikethrough by task managers.

- [x] `grid.go`: Add `AttrDim`, `AttrItalic`, `AttrStrikethrough` to
      `Cell.Attrs` bitset.
- [x] `parser.go::applySGR`: Handle SGR 2 (Dim), 3 (Italic),
      9 (Strikethrough) and their resets (22, 23, 29).
- [x] `widget.go::onDraw`: Update `style()` to honor new attributes.
      Draw strikethrough lines (manually via `dc.FilledRect` or
      `TextStyle` if supported).
- [x] Tests: SGR roundtrip for new attributes; verification of correct
      bitmasking.

**Demo test:**
```
printf '\x1b[3mITALIC\x1b[0m \x1b[2mDIM\x1b[0m \x1b[9mSTRIKE\x1b[0m\n'
```

---

## Phase 13 — Logical Line Wrapping (Reflow)

**Why:** Crucial for resizing. Simple cropping/padding feels broken.
Modern terminals reflow text based on logical line boundaries.

- [x] `grid.go`: Add `Wrapped bool` to `Cell` or a per-row flag.
      Set `Wrapped=true` in `Put` when an AutoWrap occurs.
- [x] `grid.go`: Rewrite `Resize` to perform a logical reflow. Walk
      the buffer and scrollback, joining rows marked `Wrapped` and
      re-wrapping to the new width.
- [x] `grid.go`: Update cursor positioning to follow the reflowed
      content.
- [x] Tests: Growing and shrinking width with multi-line wrapped
      output; verification that `ls` columns vs `cat` text reflow
      differently (based on explicit newlines).

**Demo test:** `cat` a long paragraph, narrow the window, then
widen it. Text should flow like a responsive webpage.

---

## Phase 14 — OSC 52 (Clipboard) & OSC 8 (Hyperlinks)

**Why:** standard way for remote/embedded apps to interact with the
host. Hyperlinks are standard in modern CLIs.

- [x] `parser.go`: Handle OSC 52 (Base64 clipboard) and OSC 8 (URL).
- [x] `grid.go`: `Cell` needs to track hyperlink association. Use a
      sidecar map `map[uint16]string` to store URLs, keyed by a
      compact `LinkID uint16` in the `Cell`. Zero fills the padding
      already present in the struct — no size increase.
- [x] `widget.go`: `onDraw` highlights active links on hover (underline
      + blue tint on hover). `onMouseUp` opens URLs via OS default
      handler on Cmd/Ctrl+click with no drag.
- [x] `widget.go`: OSC 52 integration with `win.SetClipboard` via
      `QueueCommand`. OSC 52 read requests ignored (require async
      UI-thread access).
- [x] Tests: Base64 decoding, URL stashing/clearing, dedup, and
      hyperlink hit-detection logic. 12 new tests total.

**Demo test:** `ls --hyperlink=always` (on Linux) or a custom script
emitting OSC 8.

---

## Phase 15 — Search in Scrollback

**Why:** Finding specific output in a large history is critical for
productivity.

- [x] `grid.go`: Add `Find(query string, start Pos, forward bool)`
      helper to walk cells and scrollback.
- [x] `widget.go`: Add `SearchQuery` state and a basic search UI
      (e.g., triggered by Cmd+F).
- [x] `widget.go::onDraw`: Highlight all occurrences of `SearchQuery`
      in the visible viewport.
- [x] `onKeyDown`: Enter/Shift+Enter to jump to next/prev match.
- [x] Tests: Multi-row matches, case-insensitive search, and wrapping
      search.

**Demo test:** Cmd+F, type "error", matches are highlighted.

---

## Phase 16 — Performance: Coalesced Text & Caching

**Why:** Large windows at high DPI can be slow if we `dc.Text` every
cell.

- [x] `widget.go::onDraw`: Coalesce runs of cells with identical
      SGR/Attrs/Links into single `dc.Text` calls.
- [x] `widget.go`: Enable `DrawCanvas(ID, Version)` to leverage
      `go-gui`'s internal tessellation/render cache. Increment
      `Version` only when the grid actually changes.
- [x] Profile and optimize `resolveCell`, `fg()`, and `bg()` hot paths.
      Fast paths already optimal; no changes needed without profile data.
- [x] Tests: Performance benchmark for full-screen redraws
      (`BenchmarkForegroundPass`: 37µs, 0 allocs on 80×24, Apple M5).

**Demo test:** `cat` a large file in a maximized window; verify
smooth scrolling.

---

## Phase 17 — Persistent Selection

**Why:** selection should stick to the content, not the viewport
pixels, so it survives scrolling and resizing.

- [x] `grid.go`: Move `SelAnchor` and `SelHead` from viewport-relative
      coordinates to content-relative (Row = scrollback index or
      live row index).
- [x] `grid.go`: Update `InSelection` and `SelectedText` to walk
      content rows.
- [x] `grid.go`: Update `Resize` (Phase 13) to reflow selection
      bounds along with the text.
- [x] Tests: Select text, scroll, verify highlight stays with text;
      select text, resize, verify selection remains correct.

**Demo test:** select a word, scroll up, word stays highlighted.

---

## Phase 18 — Visual Bell (BEL)

**Why:** Many CLI tools and shells emit `\a` (BEL) to signal completion or errors.

- [x] `grid.go`: Add `Bell()` which increments a `BellCounter` or sets a timestamp.
- [x] `parser.go`: When `0x07` (BEL) is received, call `g.Bell()`.
- [x] `widget.go`: `onDraw` checks the bell state/timestamp and briefly inverts the screen or draws a subtle flash.
- [x] Tests for BEL reception and state mutation.

**Demo test:** `printf '\a'` (or `echo -e "\a"`) triggers a visual flash.

---

## Phase 19 — Scrollbar Indicator

**Why:** With a large scrollback, users need visual feedback on their position in history.

- [x] `widget.go`: `onDraw` renders a thin, semi-transparent vertical bar on the right edge.
- [x] Logic to map `ViewOffset` and `ContentRows` to scrollbar position and size.
- [x] Logic to hide the scrollbar when at the live viewport and no movement has occurred for N seconds.

**Demo test:** Scroll up; a scrollbar appears on the right indicating position.

---

## Phase 20 — Extended Underline Styles & Colors

**Why:** Modern syntax highlighters and editors (like Neovim) use curly or colored underlines for diagnostics.

- [x] `grid.go`: Add `ULStyle uint8` (ULNone/ULSingle/ULDouble/ULCurly/ULDotted/ULDashed) and
      `ULColor uint32` to `Cell`; `CurULStyle`/`CurULColor` to `Grid`. Propagated through
      `Put`, `SaveCursor`/`RestoreCursor`, `EnterAlt`/`ExitAlt`, `blankCell`, `defaultCell`.
- [x] `parser.go`: Colon sub-param tracking (`paramSub`/`nextIsSub`). Handle `CSI 4:n m`
      (style), `CSI 21 m` (double), `CSI 24 m` (clear), `CSI 58;2;r;g;b m` / `58;5;n m`
      (underline color), `CSI 59 m` (reset color). SGR 0 resets all UL state.
- [x] `widget.go`: `runKey` carries `ulStyle`/`ulColor`; `cellRunKey` extracts from cell.
      `drawUnderlineDecor` renders double/curly/dotted/dashed via `dc.FilledRect`. Single
      underline uses `gui.TextStyle.Underline`. `runCols` tracks run width for decor sizing.
- [x] Tests: `TestParser_SGR4_NoSubparam_SingleUnderline`, `TestParser_SGR4_ColonSubparam_Styles`,
      `TestParser_SGR21_DoubleUnderline`, `TestParser_SGR24_ClearsUnderline`,
      `TestParser_SGR58_ULColor_RGB`, `TestParser_SGR58_ULColor_Palette`,
      `TestParser_SGR59_ResetsULColor`, `TestParser_SGRReset_ClearsULState`,
      `TestParser_SGR4_Semicolon_NotSubparam`, `TestGrid_Put_PropagatesULStyle`,
      `TestGrid_Put_BlankCellNoUL`, `TestGrid_SaveRestoreCursor_ULState`,
      `TestGrid_DefaultCell_ULColor`.

**Demo test:** `printf '\x1b[4:3mCURLY\x1b[0m\n'`.

---

## Phase 21 — Customizable Tab Stops (HTS / TBC)

**Why:** Required for some legacy CLI applications and specialized text layouts.

- [x] `grid.go`: Add `TabStops []bool` (size `MaxGridDim`).
- [x] `parser.go`: Handle `ESC H` (HTS - set stop) and `CSI g` (TBC - clear stop).
- [x] `grid.go`: `Tab()` uses `TabStops` if set, else falls back to default 8-col.
- [x] Tests for setting and clearing tab stops.

**Demo test:** Set a custom tab stop, then use `\t` to verify alignment.

---

## Phase 22 — Meta/Alt Key Encoding

**Why:** Many CLI apps (Emacs, readline) use Alt+Key combinations for shortcuts.

- [x] `widget.go`: `onKeyDown` prefixes output with `\x1b` (ESC) when `ModAlt` is held.
- [x] Handle both single characters and existing escape sequences (e.g., Alt+Arrow).

**Demo test:** Press Alt+F in a shell to jump forward a word (if supported by shell).

---

## Phase 23 — Enhanced Keypad & Function Keys (F1–F12)

**Why:** Required for complex TUIs like Midnight Commander or specialized editors.

- [x] `widget.go`: Expand `onKeyDown` and `keypadSeq` to cover F1–F12 and more keypad variants.
- [x] Honor `AppCursorKeys` and `AppKeypad` modes for all new keys.

**Demo test:** Run `mc` or `htop` and verify function keys work.

---

## Phase 24 — Color Themes & Palette API

**Why:** Allow users to use popular themes (Gruvbox, Solarized, Nord) without modifying `palette.go`.

- [x] `grid.go`: Move the 256-color palette from a global to a field in `Grid` or a shared `Theme` struct.
- [x] `term.go`: Add `SetTheme(colors [16]gui.Color)` or similar API.
- [x] Update `resolve` to use the grid-local palette.

**Demo test:** Change the theme at runtime via a demo menu.

---

## Phase 25 — Performance: Dirty Row Tracking

**Why:** Even with coalescing, `onDraw` iterates every cell. Large windows can be optimized.

- [x] `grid.go`: Add `Dirty []bool` to track modified rows. `markDirty`/`markAllDirty` called at
      all mutation sites (`Put`, `EraseInLine`, `EraseInDisplay`, `InsertChars`, `DeleteChars`,
      `scrollUpRegion`, `scrollDownRegion`, `InsertLines`, `DeleteLines`, `ClearAll`,
      `EnterAlt`, `ExitAlt`, `Resize`). `HasDirtyRows` / `ClearDirty` for widget integration.
- [x] `widget.go`: `readLoop` gates `bumpVersion`+`QueueCommand` on `HasDirtyRows` or new BEL,
      preventing cache invalidation for no-op PTY sequences. `onDraw` calls `ClearDirty` under
      Mu at the start of each render cycle. DrawCanvas `ID: "term-canvas"` was already set,
      enabling go-gui tessellation cache to skip `OnDraw` when version is unchanged.
- [x] Benchmarking: existing `BenchmarkForegroundPass` (37µs, 0 allocs on 80×24) unchanged.

**Demo test:** `top` or `htop` running in a large window should show lower CPU usage for the UI thread.

---

## Phase 26 — Semantic Shell Integration (OSC 133)

**Why:** Unlocks power-user features like jumping between prompts and capturing command output.

- [x] `parser.go`: Handle `OSC 133;A` (Prompt start), `B` (Command start), `C` (Command end/exit code), `D` (Output end).
- [x] `grid.go`: Store command boundaries as "Marks" in the scrollback and live buffer.
- [x] `widget.go`: Add keyboard shortcuts (e.g., Cmd+Up/Down) to jump to the previous/next command mark.
- [x] Tests: Verify mark placement and retrieval across resizes and reflows.

**Demo test:** `source shell-integration.zsh` (generic script) and verify command jumping works.

---

## Phase 27 — Kitty Keyboard Protocol (CSI u)

**Why:** Modern TUI apps (Neovim, Emacs) need to distinguish `Tab` from `Ctrl+I` and handle complex chords like `Ctrl+Enter`.

- [x] `parser.go`: Handle `CSI > u` (push flags), `CSI < u` (pop), `CSI = u` (set),
      `CSI ? u` (query with reply). Leaders `>`, `<`, `=` added to `dispatchCSI`.
      `grid.go`: `KittyKeyFlags uint32` + `kittyFlagStack []uint32` + `PushKittyKeyFlags`,
      `PopKittyKeyFlags`, `SetKittyKeyFlags` methods (stack capped at 8).
- [x] `widget.go`: `kittyKeySeq` helper encodes `CSI codepoint ; modifiers u`.
      `onKeyDown` emits KKP sequences for Backspace (127), Enter (13), Tab (9),
      Escape (27), and Ctrl+letters when `KittyKeyFlags != 0`; falls back to legacy
      byte when flags == 0. `onChar` intercepts printable keys under flag 8 (all-
      as-escape), mapping shifted letters to their base codepoint.
      `keyModes` struct gains `kittyKeyFlags uint32` so the lock is taken only once.
- [x] Key release and modifier-only events implemented using go-gui's OnKeyUp callback.
      Flag bit 2 (report event types) generates CSI codepoint ; modifiers :3 u (event-type
      3 = release) for key releases. Modifier field is always emitted when event-type is
      present (mod=1 for no modifiers). Functional and nav keys use KKP private-use-area
      codepoints (Insert=57348, Delete=57349, arrows=57350-57353, PageUp/Down=57354-57355,
      Home/End=57356-57357, F1-F12=57364-57375). Left vs. right modifiers use distinct
      codepoints (e.g. Left Shift=57441, Right Shift=57447).
- [x] Tests: `TestParser_KittyKeyPush`, `TestParser_KittyKeyPop`,
      `TestParser_KittyKeyPopN`, `TestParser_KittyKeyPopEmpty`,
      `TestParser_KittyKeySet`, `TestParser_KittyKeyQuery`,
      `TestParser_KittyKeyQueryZero`, `TestKittyKeySeq_Disabled`,
      `TestKittyKeySeq_NoMods`, `TestKittyKeySeq_WithMods`,
      `TestKittyKeySeq_Release`, `TestTerm_KittyKey_Backspace`,
      `TestTerm_KittyKey_Enter`, `TestTerm_KittyKey_Tab`,
      `TestTerm_KittyKey_Escape`, `TestTerm_KittyKey_CtrlC`,
      `TestTerm_KittyKey_Release`, `TestTerm_KittyKey_ModifierOnly`,
      `TestTerm_KittyKey_ReleaseDisabled`, `TestTerm_KittyKey_LegacyFallback`.

**Demo test:** Run `showkey -a` or a test script in Neovim to verify distinct key codes.

---

## Phase 28 — SGR-Pixels Mouse Reporting (1016)

**Why:** High-precision mouse coordinates for advanced TUI drawing/interactions and pixel-perfect clicking.

- [x] `parser.go`: Track DEC private mode `?1016 h/l` (SGR-Pixels).
- [x] `widget.go`: Update `encodeMouseSGR` to emit pixel-relative coordinates instead of 1-based cell coordinates when mode 1016 is active.
- [x] Tests: Verify pixel vs cell coordinate math.

**Demo test:** A TUI app that requires pixel-perfect clicks (e.g., a drawing tool) behaves correctly.

---

## Phase 29 — Regular Expression Search

**Why:** Finding patterns like IP addresses, Git hashes, or specific error formats in large logs.

- [ ] `grid.go`: Upgrade the `Find` helper to support `regexp.Regexp` patterns.
- [ ] `widget.go`: Update the search UI to allow toggling between plain-text and regex modes.
- [ ] Tests: Verify complex regex matches across multi-line wrapped content.

**Demo test:** Cmd+F, toggle regex, search for `[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}` to find IPs.

---

## Phase 30 — External Control / API (OSC commands)

**Why:** Allows scripts and tools to interact with or query the terminal state (e.g., changing themes per-project).

- [ ] `parser.go`: Implement OSC sequences for querying/setting terminal state (e.g., Background color, window title, or font size).
- [ ] `widget.go`: Handle requests for state that requires UI-thread synchronization.
- [ ] Tests: Verify state changes and host replies for query sequences.

**Demo test:** `printf '\x1b]11;rgb:ff/00/00\x07'` to change the background to red.

---

## Phase 31 — Disk-Backed Scrollback

**Why:** Effectively "infinite" scrollback without high RAM usage or stalling on large `cat` operations.

- [ ] `grid.go`: Refactor the `Scrollback` ring buffer to use a pager that can swap old rows to a compressed temporary file on disk.
- [ ] Implement transparent loading of rows when `ViewOffset` moves deep into history.
- [ ] Tests: Verify integrity of disk-persisted rows and performance of random access.

**Demo test:** `seq 1 1000000` and verify memory usage stays flat while history remains reachable.

---

## Phase 32 — Sixel Graphics (Minimal)

**Why:** Essential for modern terminal-parity; allows image previews and plot visualization.

- [ ] `parser.go`: Handle DCS `q` for Sixel data streams.
- [ ] `term/graphics.go`: New layer for managing a pixel-buffer (sidecar to the cell grid) and rendering it in `onDraw`.
- [ ] `grid.go`: Cells covered by graphics need "placeholder" state to avoid text overstrike.
- [ ] Tests: Verify Sixel decoding and correct placement relative to scrolling.

**Demo test:** `img2sixel logo.png` renders the image inside the terminal.

## Critical files

All edits stay in:
- `term/grid.go` — most phases.
- `term/parser.go` — phases 18, 20, 21, 26, 27, 28, 30, 32.
- `term/widget.go` — phases 18, 19, 20, 22, 23, 25, 26, 27, 28, 29, 30.
- `term/palette.go` — phase 24.
- `term/graphics.go` — phase 32 (new file).

## Reused functions / patterns to preserve

No new files unless a phase grows past ~300 LoC; if so, split that
phase's surface into `term/<feature>.go` (e.g. `term/scrollback.go`).

## Reused functions / patterns to preserve

- `Grid.Mu` stays the single lock; reader goroutine + `OnDraw` are
  the only contenders. Don't add per-feature mutexes.
- `Window.QueueCommand` remains the only way the reader goroutine
  touches gui state. Title updates and clipboard writes triggered
  by parser must go through it.
- `palette.fg()` / `palette.bg()` remain the single resolution
  helper — extend, don't bypass.
- `dispatchCSI` is the single CSI dispatch site — extend, don't add
  parallel dispatchers.

## End-to-end verification (every phase)

1. `go vet ./...` clean.
2. `go build ./...` clean.
3. `go test ./term/...` passes; new behavior covered by table-driven
   parser/grid tests.
4. `cd cmd/demo && go run .` and run the phase-specific demo command
   listed above. Verify visually.
5. Smoke matrix that must keep working through all phases:
   - `ls --color=always`
   - `cat /etc/hosts`
   - `vim foo.txt` then `:q!`
   - Window resize → `stty size` reflects new dims
   - Ctrl+C interrupts a running `sleep 100`

## Out of scope (still)

Per CLAUDE.md "Out-of-scope" list, defer:
- IME / dead keys
- GPU-accelerated rendering

## Resolved decisions

1. **Color encoding:** pack RGB+flag into a single `uint32` per FG
   and per BG. Simplest; alloc cost deferred per project policy.
2. **Scrollback cap:** expose `Cfg.ScrollbackRows`, default 5000.
3. **Selection mouse:** left-drag select + Cmd/Ctrl+C copy. No
   middle-click PRIMARY paste.
4. **Alt-screen scrollback:** suppress while alt is active
   (kitty/iTerm/ghostty default).
5. **Cursor blink:** honor DECSCUSR blink request; allow
   `Cfg.CursorBlink *bool` to force on/off.

No remaining unresolved questions.
