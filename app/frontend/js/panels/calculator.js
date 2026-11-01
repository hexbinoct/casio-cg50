// The Calculator window: the live screen and the keypad (the whole app before the
// workspace). One instance. Real data: the screen comes from Go's /frame, keys go to Go.
//
// Interface:
//   create(ctx)          the window (workspace.js)
//   keydown(e) / keyup(e) computer keys while this window has the keyboard (app.js)
//   releaseAll()          let go of every held key (focus left the window or the app)
import * as keypad from "../keypad.js";
import { HOLD, letter } from "../keys.js";
import { h, icon } from "./pane.js";

export const meta = { kind: "calculator", title: "Calculator", multi: false };

const W = 396, H = 224; // the whole panel (cg50emu.PanelWidth × PanelHeight)
const go = () => window.go?.main?.App;

// Key events go to Go strictly one after another: Wails runs each call on its own goroutine,
// so a fast tap's KeyUp could otherwise overtake its KeyDown and leave the key held.
let keyQueue = Promise.resolve();
function sendKey(name, ...args) {
  keyQueue = keyQueue.then(() => go()?.[name](...args)).catch(() => {});
}

let view = null; // the one live window
const held = new Map(); // computer key code → [row, col]

export function keydown(e) {
  if (e.ctrlKey || e.metaKey) return false;
  e.preventDefault();
  if (e.repeat) return true; // the calculator repeats held keys itself
  const rc = HOLD[e.key];
  if (rc) {
    held.set(e.code, rc);
    if (view) keypad.show(view.pad, rc[0], rc[1], true);
    sendKey("KeyDown", rc[0], rc[1]);
    return true;
  }
  const seq = letter(e.key);
  if (seq && !e.altKey) sendKey("Tap", seq);
  return true;
}

export function keyup(e) {
  const rc = held.get(e.code);
  if (!rc) return false;
  held.delete(e.code);
  if (view) keypad.show(view.pad, rc[0], rc[1], false);
  sendKey("KeyUp", rc[0], rc[1]);
  return true;
}

export function releaseAll() {
  if (view) for (const rc of held.values()) keypad.show(view.pad, rc[0], rc[1], false);
  view?.pad.querySelectorAll(".down").forEach((b) => b.classList.remove("down"));
  held.clear();
  sendKey("ReleaseAllKeys");
}

export function create(ctx) {
  const el = h("div.pane.pane-calculator", { tabIndex: 0 });
  const canvas = h("canvas.lcd", { width: W, height: H });
  const pad = h("div.keypad");
  const device = h("div.device",
    {},
    h("div.screen-col", {}, h("div.screen-well", {}, canvas), h("div.engrave", { text: "CG50 Studio" })),
    pad);
  // the keypad toggle: auto (shown when it fits at full size), always, never
  const modes = ["auto", "on", "off"];
  const names = { auto: "Keypad: when it fits", on: "Keypad: always", off: "Keypad: hidden (screen only)" };
  let mode = modes.includes(ctx.params.keypad) ? ctx.params.keypad : "auto";
  const toggle = h("button.corner-btn", {
    onclick: (e) => {
      e.stopPropagation();
      mode = modes[(modes.indexOf(mode) + 1) % modes.length];
      ctx.save({ keypad: mode });
      toggle.title = names[mode];
      toggle.dataset.mode = mode;
      fit();
    },
  });
  toggle.append(icon("keypad", 15));
  toggle.title = names[mode];
  toggle.dataset.mode = mode;
  el.append(device, toggle);
  keypad.build(pad, (r, c) => sendKey("KeyDown", r, c), (r, c) => sendKey("KeyUp", r, c));
  // a click anywhere in the window gives it the keyboard (keypad buttons have tabIndex -1)
  el.addEventListener("pointerdown", () => { if (document.activeElement !== el) el.focus({ preventScroll: true }); });

  // ---- screen ------------------------------------------------------------------------
  const g = canvas.getContext("2d");
  let gen = "", fetching = false, visible = true, alive = true, idleUntil = 0;
  async function pullFrame() {
    if (fetching || !visible || performance.now() < idleUntil) return;
    fetching = true;
    try {
      const r = await fetch("/frame?since=" + gen, { cache: "no-store" });
      if (r.status === 404) idleUntil = performance.now() + 2000; // no Go side (a plain web server)
      if (r.status === 200) {
        const buf = await r.arrayBuffer();
        if (buf.byteLength === W * H * 4) {
          g.putImageData(new ImageData(new Uint8ClampedArray(buf), W, H), 0, 0);
          gen = r.headers.get("X-Frame-Gen") || "";
        }
      }
    } catch (_) { /* the app is starting or closing */ }
    fetching = false;
  }
  (function loop() { if (!alive) return; pullFrame(); requestAnimationFrame(loop); })();

  // ---- fit the device to the window --------------------------------------------------
  // Device size in screen-scale units (see app.css): 658 wide (28 + 414 screen well + 10 + 206
  // keypad), about 277 tall (the screen column 253 + 24 padding). Screen only: the well,
  // 414 × 242. Scales snap to whole device pixels when close, so the LCD's pixels stay crisp
  // on HiDPI screens too.
  function snap(s) {
    const dpr = window.devicePixelRatio || 1;
    let d = s * dpr;
    const i = Math.floor(d);
    d = i >= 1 && d - i < 0.3 ? i : Math.floor(d * 4) / 4;
    return Math.max(d, 0.25) / dpr;
  }
  function fit() {
    const w = el.clientWidth, hgt = el.clientHeight;
    if (!w || !hgt) return;
    const full = Math.min((w - 40) / 658, (hgt - 36) / 280);
    const withPad = mode === "on" || (mode === "auto" && full >= 1);
    let s;
    if (withPad) s = snap(Math.max(1, full));
    else s = snap(Math.max(0.5, Math.min((w - 24) / 414, (hgt - 24) / 242)));
    device.classList.toggle("bare", !withPad);
    el.style.setProperty("--s", s);
  }
  const ro = new ResizeObserver(fit);
  ro.observe(el);
  matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`).addEventListener?.("change", fit);

  view = { pad, el };
  return {
    element: el,
    focus: () => el.focus({ preventScroll: true }),
    onShow: () => { visible = true; gen = ""; fit(); },
    onHide: () => { visible = false; },
    dispose: () => { alive = false; ro.disconnect(); releaseAll(); if (view?.el === el) view = null; },
    keydown, keyup,
  };
}
