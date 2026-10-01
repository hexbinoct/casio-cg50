# Plan: the emulator as the everyday test device (desktop app + web via WASM)

*Written 2026-09-30. Status: idea, agreed direction, nothing built yet.*

## Why

The emulator now runs the real OS from a full 32 MB dump of our own calculator: it warm-boots
straight to that calculator's MAIN MENU, and gint add-ins (timers, INTC, DMA, BFile file
access) run on it. Add-in work so far went build → copy over USB → try on the calculator. With
the emulator this complete, the loop can be **build → run in the emulator on the computer**,
in a window that looks and feels like the calculator. The real calculator stays the final
check before a release: the emulator is not perfect. For example, cont.18x fixed an interrupt
bug that only showed up after thousands of world switches.

Target experience:

- **One command or a double-click** opens a good-looking calculator window: the fx-CG50 skin,
  a clickable keypad, the keyboard mapped, and the screen scaled sharply.
- **Drop a `.g3a` on it** (or point it at a build folder) and the add-in is installed and
  launched. Rebuilding re-installs it.
- Save and load states, a speed setting, screenshots of the whole 396×224 add-in frame, and a
  key-script box (the `DASM_KEYS` idea from the probes).
- **The same app in a browser** through WebAssembly: no install, works on any machine.

## What exists already

| Piece | Where | State |
|---|---|---|
| Core (CPU, MMU, INTC, timers, DMAC, LCD, KEYSC, RTC, NOR flash) | `emu_go/` | pure Go, 64–85 M instr/s native |
| Warm boot from a full dump, own save-state | `CG50_FLASH`, `CG50_STATE`, `CG50_WARM` | done (cont.18w) |
| Browser UI | `emu_go/webui.go`, `go -C emu_go run . 0 30000 web` | works but plain: stdlib server, PNG frames polled, keyboard only |
| Calculator skin + keypad + haptics + save-state on pause | `android/` (`KeypadView.kt`, `KeyMap.kt`) | done on the phone; the layout data can be reused |
| Running a fresh `.g3a` | probes `dasm_swap_test.go` / `dasm_real_test.go` | a hack: swap code pages into an add-in that is already installed |
| WASM build | `GOOS=js GOARCH=wasm go build` in `emu_go/` | **compiles as-is** (checked 2026-09-30, 11 MB before trimming); the cgo part is Android-only |

## The pieces to build

### 1. Install any `.g3a` properly (the key piece)

> **Done (2026-10-01, cont.18y):** `Emulator.InstallAddin` (`emu_go/install.go`). A variant of
> (b) without an installer add-in: the emulator calls the OS's own Bfile syscalls in the OS's
> idle main context (`oscall.go`), then the OS rebuilds its add-in table and the MAIN MENU is
> re-entered so the icon appears. Used by the Android app (file picker, Open with, adb). Needs
> the 32 MB dump.

Today a new add-in can only run by hot-swapping its code into one already in the dump. The file
has to really exist in storage, so the OS lists it in the menu and its BFile calls see it. The
options:

- **(a) Write the Fugue file system ourselves:** a big job, and we would own every FS detail.
- **(b) Let the OS write it (recommended):** a tiny *installer* add-in, run inside the emulator,
  copies the bytes the host placed in a spare RAM window to `\\fls0\NAME.g3a` using the OS's
  own `BFile_Create`/`BFile_Write`, then returns to the MAIN MENU. The OS keeps the file system
  consistent itself. Getting the installer in the first time uses the existing swap trick once
  (or it is installed once and kept in the save-state).
- (c) The swap trick as the product: fragile, and the add-in never really exists as a file.

### 2. One front-end for desktop and web

A single HTML/JS/canvas front-end used by both builds:

- **Desktop:** the Go program serves the page and opens it in an app-style window. Upgrade
  `webui.go`: WebSocket or streamed frames instead of PNG polling, the calculator skin with
  clickable keys (the Android `KeyMap` layout), drag-and-drop `.g3a`, state/speed/screenshot
  buttons. A macOS `.app` wrapper, a Windows shortcut, or a tiny launcher gives the double-click.
  (A native Go window with Ebiten or Wails is possible later, but a shared web front-end means
  building the UI once.)
- **Web:** the same page with the core compiled to WASM, running in a Web Worker so the UI stays
  smooth, and frames posted to the page.

### 3. WASM specifics

- **Speed:** Go's WASM output is usually 2–4× slower than native, so expect roughly 20–40 M
  instr/s. That is around the calculator's own pace (the Android "original" speed setting is
  45 M), and the idle OS sleeps, so menus stay light. Heavy add-ins may run slower than on the
  real calculator. TinyGo, or hot-path tuning, if needed.
- **No Casio firmware on the site.** The page ships only our code. Each user opens their own
  flash dump with a file picker. It stays in the browser (IndexedDB) and is never uploaded.
  Save-states are kept there too. The static page can then live on GitHub Pages from this repo,
  in line with the repo's no-firmware rule.
- **Trim the binary:** a WASM-only entry point without the RE/diagnostic modes of `main.go`.

## Suggested order

1. **The installer** (1b): unlocks "run any `.g3a`" for every later step.
2. **The desktop app:** the upgraded web front-end served by Go, drag-and-drop install,
   one-click launch.
3. **The WASM build + GitHub Pages:** the same front-end, core in a Worker, a local-only
   firmware picker.

Each step keeps the existing gate: `go -C emu_go test -count=1 .` stays green and new
behaviour gets tests.
