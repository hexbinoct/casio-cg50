// CG50 Studio page: draws the screen, sends keys, and drives the Go side
// (window.go.main.App, bound by Wails; events through window.runtime).
import * as keypad from "./keypad.js";
import { HOLD, HELP, letter } from "./keys.js";

const W = 396, H = 224; // the whole panel (cg50emu.PanelWidth × PanelHeight)
const $ = (id) => document.getElementById(id);
const go = () => window.go.main.App;

// ---- calls with error toasts ---------------------------------------------------------
function call(name, ...args) {
  return go()[name](...args).catch((err) => toast("error", String(err)));
}

function toast(kind, text) {
  const t = document.createElement("div");
  t.className = "toast" + (kind === "error" ? " error" : "");
  t.textContent = text;
  $("toasts").append(t);
  setTimeout(() => t.remove(), kind === "error" ? 7000 : 3500);
}

// ---- screen --------------------------------------------------------------------------
const ctx = $("screen").getContext("2d");
let gen = "";
let fetching = false;

async function pullFrame() {
  if (fetching) return;
  fetching = true;
  try {
    const r = await fetch("/frame?since=" + gen, { cache: "no-store" });
    if (r.status === 200) {
      const buf = await r.arrayBuffer();
      if (buf.byteLength === W * H * 4) {
        ctx.putImageData(new ImageData(new Uint8ClampedArray(buf), W, H), 0, 0);
        gen = r.headers.get("X-Frame-Gen") || "";
      }
    }
  } catch (_) { /* the app is starting or closing */ }
  fetching = false;
}
(function loop() { pullFrame(); requestAnimationFrame(loop); })();

// ---- fit the device to the window ----------------------------------------------------
// Device size in screen-scale units (see app.css): 658 wide (28 + 414 screen well + 10 + 206
// keypad), about 277 tall (the screen column 253 + 24 padding).
function fit() {
  const st = $("stage").getBoundingClientRect();
  let s = Math.min((st.width - 40) / 658, (st.height - 36) / 280);
  s = Math.max(1, s);
  const i = Math.floor(s);
  s = s - i < 0.3 ? i : Math.floor(s * 4) / 4; // whole-pixel scales when close: crisp pixels
  document.documentElement.style.setProperty("--s", s);
}
window.addEventListener("resize", fit);
fit();

// ---- keypad and keyboard -------------------------------------------------------------
// Key events go to Go strictly one after another: Wails runs each call on its own goroutine,
// so a fast tap's KeyUp could otherwise overtake its KeyDown and leave the key held.
let keyQueue = Promise.resolve();
function sendKey(name, ...args) {
  keyQueue = keyQueue.then(() => go()[name](...args)).catch(() => {});
}

const pad = $("keypad");
keypad.build(pad, (r, c) => sendKey("KeyDown", r, c), (r, c) => sendKey("KeyUp", r, c));

const held = new Map(); // computer key → [row, col]
const shortcuts = {
  s: () => call("SaveSlot", 1), l: () => call("LoadSlot", 1), o: () => call("OpenDump"),
  i: () => call("InstallAddin"), p: () => call("Screenshot", 1), r: () => call("Restart"),
};

document.addEventListener("keydown", (e) => {
  if ($("help").open) return;
  if (e.ctrlKey || e.metaKey) {
    const f = shortcuts[e.key.toLowerCase()];
    if (f) { e.preventDefault(); if (!e.repeat) f(); }
    return;
  }
  if (e.key === "F8") { e.preventDefault(); if (!e.repeat) togglePause(); return; }
  e.preventDefault();
  if (e.repeat) return; // the calculator repeats held keys itself
  const rc = HOLD[e.key];
  if (rc) {
    held.set(e.code, rc);
    keypad.show(pad, rc[0], rc[1], true);
    sendKey("KeyDown", rc[0], rc[1]);
    return;
  }
  const seq = letter(e.key);
  if (seq && !e.altKey) sendKey("Tap", seq);
});

document.addEventListener("keyup", (e) => {
  const rc = held.get(e.code);
  if (!rc) return;
  held.delete(e.code);
  keypad.show(pad, rc[0], rc[1], false);
  sendKey("KeyUp", rc[0], rc[1]);
});

window.addEventListener("blur", () => {
  for (const rc of held.values()) keypad.show(pad, rc[0], rc[1], false);
  held.clear();
  sendKey("ReleaseAllKeys");
});

// ---- top bar -------------------------------------------------------------------------
let status = { loaded: false, paused: false, speed: "1x" };

function togglePause() { call("SetPaused", !status.paused); status.paused = !status.paused; render(); }

$("runBtn").onclick = togglePause;
$("dumpChip").onclick = () => call("OpenDump");
$("welcomeOpen").onclick = () => call("OpenDump");
$("saveBtn").onclick = () => call("SaveSlot", 1);
$("loadBtn").onclick = () => call("LoadSlot", 1);
$("shotBtn").onclick = () => call("Screenshot", 1);
$("installBtn").onclick = () => call("InstallAddin");
document.querySelectorAll("#speedSeg button").forEach((b) => {
  b.onclick = () => { status.speed = b.dataset.speed; call("SetSpeed", b.dataset.speed); render(); };
});

const menu = $("menu");
$("menuBtn").onclick = (e) => { e.stopPropagation(); menu.classList.toggle("open"); };
document.addEventListener("click", () => menu.classList.remove("open"));
const actions = {
  openDump: () => call("OpenDump"),
  saveAs: () => call("SaveStateAs"),
  openState: () => call("OpenState"),
  shot2: () => call("Screenshot", 2),
  shot3: () => call("Screenshot", 3),
  restart: () => call("Restart"),
  forget: () => {
    if (confirmForget()) call("ForgetChanges");
  },
  help: () => $("help").showModal(),
};
menu.querySelectorAll("[data-act]").forEach((b) => { b.onclick = () => actions[b.dataset.act](); });

// A second click within 4 s confirms (no blocking browser dialogs inside the app).
let forgetArmed = 0;
function confirmForget() {
  if (Date.now() - forgetArmed < 4000) { forgetArmed = 0; return true; }
  forgetArmed = Date.now();
  toast("error", "This drops everything stored since the dump (installed add-ins, settings, the saved session). Choose it again to confirm.");
  return false;
}

// keyboard help
$("keysTable").innerHTML = HELP.map(([k, v]) => `<div><kbd>${k}</kbd><span>${v}</span></div>`).join("");
$("helpClose").onclick = () => $("help").close();

// ---- status --------------------------------------------------------------------------
function render() {
  const s = status;
  $("welcome").hidden = s.loaded;
  $("dumpName").textContent = s.loaded ? `${s.dump} · ${s.dumpMB} MB` : "No flash dump";

  const run = $("runBtn");
  const showRun = s.paused || !s.loaded;
  run.querySelector("use").setAttribute("href", showRun ? "#i-play" : "#i-pause");
  run.querySelector("span").textContent = showRun ? "Run" : "Pause";
  run.classList.toggle("primary", s.paused || !s.loaded);
  for (const id of ["runBtn", "saveBtn", "loadBtn", "shotBtn", "installBtn"]) $(id).disabled = !s.loaded;
  document.querySelectorAll("#speedSeg button").forEach((b) => b.classList.toggle("on", b.dataset.speed === s.speed));

  let state = "No dump", cls = "";
  if (s.loaded) {
    if (s.fault) { state = "Stopped"; cls = "fault"; }
    else if (s.installing) { state = "Installing…"; cls = "booting"; }
    else if (s.paused) { state = "Paused"; cls = "paused"; }
    else if (s.booting) { state = "Starting…"; cls = "booting"; }
    else { state = "Running"; cls = "running"; }
  }
  $("dot").className = "dot " + cls;
  $("stateText").textContent = state;
  const live = s.loaded && !s.paused && !s.fault;
  $("speedText").textContent = live && s.percent ? `${Math.round(s.percent)}% of real speed` : "";
  $("mipsText").textContent = live && s.mips ? `${s.mips.toFixed(1)} MIPS` : "";
  $("pcText").textContent = s.loaded ? `PC ${s.pc}` : "";
  $("faultText").textContent = s.fault || "";
  $("faultText").title = s.fault || "";
}

function onStatus(s) { status = s; render(); }

window.addEventListener("DOMContentLoaded", () => {});
function boot() {
  if (!window.go || !window.runtime) { setTimeout(boot, 30); return; }
  window.runtime.EventsOn("status", onStatus);
  window.runtime.EventsOn("toast", (t) => toast(t.kind, t.text));
  go().GetStatus().then(onStatus);
}
boot();
render();
