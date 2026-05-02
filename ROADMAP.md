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

- [ ] `grid.go`: `SaveCursor()`, `RestoreCursor()` storing
      `(CursorR, CursorC, CurFG, CurBG, CurAttrs)` in a `savedCursor`
      field. Add `CursorVisible bool` (default true).
- [ ] `parser.go::dispatchCSI`: add `s` (save), `u` (restore). Add
      ESC 7 / ESC 8 in `Feed` ESC dispatch. Add DEC private mode
      `?25 h/l`.
- [ ] `widget.go::onDraw`: skip cursor block when
      `g.CursorVisible == false`.
- [ ] Tests for save/restore round-trip and `?25` toggling.

**Demo test:** `tput civis` then `tput cnorm`; press Ctrl+L while in
shell.

---

## Phase 3 — Scrollback ring buffer + mouse wheel + PgUp/PgDn

**Why:** without scrollback the terminal feels broken. Highest UX
impact for the size of change.

- [ ] `Cfg.ScrollbackRows int` (default 5000) plumbed into `New`.
- [ ] `grid.go`: `Scrollback [][]Cell` with cap from Cfg. `scrollUp()`
      pushes the dropped top row, trims to cap. Add `ViewOffset int`
      and `ScrollView(delta int)` (clamped).
- [ ] `widget.go::onDraw`: when `g.ViewOffset > 0`, render top
      `min(rows, ViewOffset)` rows from scrollback (newest-last), then
      the rest from live cells. Hide cursor while offset > 0.
- [ ] `widget.go`: `OnMouseScroll` handler → `g.ScrollView(±N)` +
      `win.QueueCommand` redraw. Reset `ViewOffset` to 0 on any
      keystroke that isn't a scrolling key.
- [ ] `widget.go::onKeyDown`: PgUp/PgDn → `ScrollView(±rows-1)`,
      Shift+Home/End → top/bottom of scrollback. Without Shift,
      behavior unchanged.
- [ ] Tests: scrollback fill + trim + view-offset clamp.

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

- [ ] `grid.go`: selection state `SelStart, SelEnd struct{ Row, Col
      int }; SelActive bool`. `SelectedText() string` walks rows,
      newline at row boundaries, trim trailing blanks per row (kitty
      convention).
- [ ] `widget.go`: `OnClick` (left, no mods) sets
      `SelStart=SelEnd=cell-at-pos`, `SelActive=false`.
- [ ] `widget.go`: `OnMouseMove` while button down → update `SelEnd`,
      `SelActive=true`.
- [ ] `widget.go`: click release → if `SelActive`,
      `win.SetClipboard(g.SelectedText())`.
- [ ] `widget.go::onDraw`: when cell is in selection, swap fg/bg
      (inverse) before paint.
- [ ] Helper `posToCell(x, y float32) (row, col int)` using
      `cellW/cellH`.
- [ ] `onKeyDown`: Cmd+C / Ctrl+Shift+C → copy current selection.
      Suppress propagation only when selection non-empty (Ctrl+C
      still SIGINTs the child otherwise).
- [ ] Tests for `SelectedText` row/column ranges and trailing-blank
      trimming.

**Demo test:** drag-select `pwd` output; paste in another app.

---

## Phase 5 — Paste (clipboard + bracketed paste mode)

**Why:** depends on Phase 4 for clipboard plumbing. Bracketed paste
(DECSET 2004) prevents shell auto-execution of pasted newlines.

- [ ] `parser.go`: track `BracketedPaste bool`; handle `?2004 h/l`
      DEC private mode.
- [ ] `widget.go`: Cmd+V / Ctrl+Shift+V in `onKeyDown` →
      `s := win.GetClipboard()`; if `parser.BracketedPaste`, write
      `\x1b[200~` + s + `\x1b[201~` to PTY, else write raw.
- [ ] Strip embedded `\x1b[201~` markers from `s` first (security).
- [ ] Tests for marker stripping and toggle behavior.

**Demo test:** copy a multi-line block, paste at zsh/bash prompt — no
auto-execute.

---

## Phase 6 — Scroll regions + line insertion/deletion + IND/RI

**Why:** prereq for vim/less to render correctly inside the main
buffer (and for alt-screen apps). Without DECSTBM, vim repaints the
whole screen on every scroll.

- [ ] `grid.go`: add `Top, Bottom int` (scroll region, default
      `0..Rows-1`). Convert `scrollUp()` to `scrollUpRegion()` honoring
      Top/Bottom. Add `ScrollDownRegion()`, `InsertLines(n)`,
      `DeleteLines(n)`, `InsertChars(n)`, `DeleteChars(n)`.
- [ ] `parser.go::dispatchCSI`: `r` (DECSTBM), `L` (IL), `M` (DL),
      `@` (ICH), `P` (DCH), `S` (SU), `T` (SD).
- [ ] `parser.go::Feed` ESC dispatch: `D` (IND), `M` (RI), `E` (NEL).
- [ ] Tests for scroll-region edge cases (region == full screen,
      region of 1 row, IL/DL at top/bottom).

**Demo test:** `vim` opens a file, scrolling shows partial repaints
not full clears. `less /etc/services` arrows scroll smoothly.

---

## Phase 7 — Alt screen (DECSET 1049 / 47 / 1047)

**Why:** unlocks vim, htop, less, tmux full-screen rendering. Depends
on Phases 2 + 6. Decision: scrollback writes suppressed while alt is
active (kitty/iTerm/ghostty default).

- [ ] `grid.go`: `altCells []Cell, altCursorR/C, altSaved bool` plus
      saved main-screen cursor. `EnterAlt()` swaps in a fresh blank
      cell buffer; `ExitAlt()` restores.
- [ ] `parser.go::dispatchCSI`: DEC private modes `?47 h/l`,
      `?1047 h/l`, `?1049 h/l`. `?1049` calls SaveCursor before
      EnterAlt, Restore after Exit.
- [ ] Suppress scrollback writes (`scrollUpRegion`) while alt is
      active.
- [ ] Tests for enter/exit symmetry and main-screen restore.

**Demo test:** `vim`, edit, `:q!` — original prompt + history
restored exactly. `htop` runs.

---

## Phase 8 — OSC: window title + cwd

**Why:** small, high-visibility win once alt-screen apps work. go-gui
has `Window.SetTitle` (window.go:500).

- [ ] `parser.go`: new state `stateOSC`. ESC `]` enters; collect
      bytes until BEL (`\x07`) or ST (`\x1b\\`).
- [ ] Dispatch OSC `0;...` and `2;...` → `Term.win.SetTitle(...)`
      (via `QueueCommand`).
- [ ] OSC `7;file://host/path` → stash `Term.Cwd` (exposed for
      embedders).
- [ ] Drop everything else (no OSC 52, no OSC 8).
- [ ] `widget.go`: `Cfg.OnTitle func(string)` callback, defaulting
      to `win.SetTitle`.
- [ ] Tests for title parsing across BEL and ST terminators.

**Demo test:** `printf '\x1b]0;hello world\x07'` → window title
updates.

---

## Phase 9 — Mouse reporting (X10 + SGR 1006)

**Why:** required by tmux pane-click, vim mouse, midnight commander.
Depends on Phase 4 mouse wiring.

- [ ] `parser.go`: track DEC private modes `?1000 h/l` (button),
      `?1002 h/l` (button + drag), `?1003 h/l` (any-motion),
      `?1006 h/l` (SGR encoding). Default off.
- [ ] `widget.go::OnClick` / `OnMouseMove` / `OnMouseScroll`: when a
      reporting mode is active, write the encoded sequence to the
      PTY and skip local selection logic. SGR form:
      `\x1b[<{btn};{col};{row}{M|m}`.
- [ ] Suppress mouse reports while `ViewOffset > 0` (scrollback
      view).
- [ ] Tests for SGR-1006 encoding of click/release/scroll.

**Demo test:** `tmux` → click between panes. `vim` with
`:set mouse=a` → click moves cursor.

---

## Phase 10 — Cursor style (DECSCUSR) + blink

**Why:** small polish. `DECSCUSR` (`CSI Ps SP q`) chooses block /
underline / bar, blinking or steady — set by zsh/fish via vim-mode
prompts. Decision: honor DECSCUSR blink request; `Cfg.CursorBlink
*bool` overrides.

- [ ] `grid.go`: `CursorStyle uint8` (0..6 per spec), `CursorBlink
      bool`.
- [ ] `parser.go`: handle ` q` (space-q) intermediate.
- [ ] `widget.go::onDraw`: render block/underline/bar accordingly.
- [ ] Blink via `time.Since(t.cursorEpoch)` modulo 1s;
      `win.QueueCommand` redraw on a `time.AfterFunc` ticker only
      while widget has focus.
- [ ] Honor `Cfg.CursorBlink *bool` override.
- [ ] Tests for DECSCUSR style/blink parameter parsing.

**Demo test:** `printf '\x1b[6 q'` → bar cursor.

---

## Phase 11 — Wide chars + emoji (East Asian Wide)

**Why:** CJK + emoji currently overstrike. `uniseg` is already an
indirect dep.

- [ ] `grid.go::Cell`: add `Width uint8` (1 or 2). `Put(ch rune)`
      consults `uniseg.StringWidth(string(ch))`. For width-2, write
      rune in cell N, mark cell N+1 as continuation (Ch=0, Width=0).
      Wrap at edge when only 1 column remains.
- [ ] `widget.go::onDraw`: skip continuation cells. Rect-run
      coalescing must not split a wide cell.
- [ ] Tests: wide-cell wrap-at-edge, cursor advance, erase across
      wide cells.

**Demo test:** `echo 你好 🍣 hello`. Cursor advances correctly past
wide chars.

---

## Critical files

All edits stay in:
- `term/grid.go` — every phase touches it (cells, cursor, regions,
  scrollback, alt, selection, wide).
- `term/parser.go` — every phase except 4-only (selection is
  widget-only).
- `term/widget.go` — phases 3, 4, 5, 8, 9, 10, 11.
- `term/palette.go` — phase 1 only.

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
- Sixel / kitty graphics protocol
- IME / dead keys
- OSC 52 clipboard (security; selection copy in Phase 4 covers UX)
- OSC 8 hyperlinks
- Windows / ConPTY
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
