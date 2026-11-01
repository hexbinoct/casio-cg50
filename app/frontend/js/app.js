// CG50 Studio page: the top bar, the status bar, the workspace of windows (workspace.js),
// and the keyboard: keys go to the window that has the focus (the Calculator presses
// calculator keys; the others use them to move around), shortcuts work everywhere.
// Go side: window.go.main.App (bound by Wails); events through window.runtime.
import { HELP } from "./keys.js";
import * as workspace from "./workspace.js";
import * as calculator from "./panels/calculator.js";
import { log } from "./panels/console.js";
import { actions as debug } from "./debugger.js";
import * as breakpoints from "./debugger.js";
import * as engine from "./engine.js";
import { nav } from "./nav.js";

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
  log(kind === "error" ? "error" : "info", text);
}
nav.log = (kind, text) => log(kind, text);

// ---- keyboard ------------------------------------------------------------------------
// App shortcuts (Ctrl/⌘ + key) and the debugger's function keys work whatever has the
// focus; everything else goes to the window that has it. Text fields keep their keys.
const shortcuts = {
  s: () => call("SaveSlot", 1), l: () => call("LoadSlot", 1), o: () => call("OpenDump"),
  i: () => call("InstallAddin"), p: () => call("Screenshot", 1), r: () => call("Restart"),
};

const editable = (el) => el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT" || el.isContentEditable);

function fkey(e) {
  if (e.key === "F8" && !e.shiftKey) return togglePause;
  if (e.key === "F10" && (e.ctrlKey || e.metaKey)) return stepping(debug.runToCursor);
  if (e.key === "F10") return stepping(debug.stepOver);
  if (e.key === "F11" && e.shiftKey) return stepping(debug.stepOut);
  if (e.key === "F11") return stepping(debug.stepInto);
  return null;
}
const stepping = (fn) => () => { if (canStep()) fn(); };

document.addEventListener("keydown", (e) => {
  if ($("help").open) return;
  const f = fkey(e);
  if (f) { e.preventDefault(); if (!e.repeat) f(); return; }
  if (e.ctrlKey || e.metaKey) {
    const s = shortcuts[e.key.toLowerCase()];
    if (s) { e.preventDefault(); if (!e.repeat) s(); return; } // none of them edits text
  }
  if (editable(e.target)) return; // typing in a search box, the console, …
  const owner = workspace.keyOwner();
  if (!owner) return;
  if (owner.kind === "calculator") { calculator.keydown(e); return; }
  if (owner.inst.keydown?.(e)) e.preventDefault();
});

document.addEventListener("keyup", (e) => { calculator.keyup(e); });

window.addEventListener("blur", () => calculator.releaseAll());

workspace.onOwnerChange((o, was) => {
  if (was === "calculator") calculator.releaseAll();
  renderKeysHint(o);
});

function renderKeysHint(o) {
  const el = $("keysHint");
  if (!o || o.kind === "calculator") {
    el.textContent = "Drop .g3a files anywhere to install · Esc = EXIT · Home = MENU";
    el.classList.remove("other");
  } else {
    el.textContent = `Keys go to ${o.api.title} · click the calculator to type on it`;
    el.classList.add("other");
  }
}

// ---- top bar -------------------------------------------------------------------------
let status = { loaded: false, paused: false, speed: "1x" };

function togglePause() {
  if (!status.loaded) return;
  const p = !status.paused;
  call("SetPaused", p).then(() => go().GetStatus().then(onStatus));
  status.paused = p;
  render();
}
const canStep = () => status.loaded && status.paused && !status.fault;

$("runBtn").onclick = togglePause;
$("dumpChip").onclick = () => call("OpenDump");
$("welcomeOpen").onclick = () => call("OpenDump");
const DUMP_HELP = "https://github.com/hexbinoct/casio-cg50#getting-a-flash-dump";
$("dumpHelp").onclick = (e) => { e.preventDefault(); window.runtime?.BrowserOpenURL(DUMP_HELP); };
$("saveBtn").onclick = () => call("SaveSlot", 1);
$("loadBtn").onclick = () => call("LoadSlot", 1);
$("shotBtn").onclick = () => call("Screenshot", 1);
$("installBtn").onclick = () => call("InstallAddin");
document.querySelectorAll("#speedSeg button").forEach((b) => {
  b.onclick = () => { status.speed = b.dataset.speed; call("SetSpeed", b.dataset.speed); render(); };
});
document.querySelectorAll("[data-dbg]").forEach((b) => { b.onclick = () => { if (canStep()) debug[b.dataset.dbg](); }; });

// menus: one open at a time, closed by a click elsewhere or Esc
const menus = [$("menu"), $("winMenu")];
function closeMenus() { menus.forEach((m) => m.classList.remove("open")); }
$("menuBtn").onclick = (e) => { e.stopPropagation(); const o = $("menu").classList.contains("open"); closeMenus(); $("menu").classList.toggle("open", !o); };
$("winBtn").onclick = (e) => {
  e.stopPropagation();
  const o = $("winMenu").classList.contains("open");
  closeMenus();
  if (!o) { workspace.fillMenu($("winList"), closeMenus); $("winMenu").classList.add("open"); }
};
$("winList").addEventListener("click", (e) => e.stopPropagation());
document.addEventListener("click", closeMenus);
document.addEventListener("keydown", (e) => { if (e.key === "Escape" && menus.some((m) => m.classList.contains("open"))) { closeMenus(); e.stopImmediatePropagation(); } }, true);
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
$("menu").querySelectorAll("[data-act]").forEach((b) => { b.onclick = () => actions[b.dataset.act](); });

// A second click within 4 s confirms (no blocking browser dialogs inside the app).
let forgetArmed = 0;
function confirmForget() {
  if (Date.now() - forgetArmed < 4000) { forgetArmed = 0; return true; }
  forgetArmed = Date.now();
  toast("error", "This drops everything stored since the dump (installed add-ins, settings, the saved session). Choose it again to confirm.");
  return false;
}

// keyboard help; on macOS the shortcuts are ⌘ (Ctrl works too)
const mac = /Mac/.test(navigator.platform || navigator.userAgent);
const mod = (t) => (mac ? t.replace(/Ctrl\+/g, "⌘") : t);
$("keysTable").innerHTML = HELP.map(([k, v]) => (v === "" ? `<h5>${k}</h5>` : `<div><kbd>${mod(k)}</kbd><span>${v}</span></div>`)).join("");
if (mac) {
  document.querySelectorAll("[title], kbd").forEach((el) => {
    if (el.title) el.title = mod(el.title);
    if (el.tagName === "KBD") el.textContent = mod(el.textContent);
  });
}
$("helpClose").onclick = () => $("help").close();

// ---- status --------------------------------------------------------------------------
function render() {
  const s = status;
  $("welcome").hidden = s.loaded || s.loading; // loading: the remembered dump is opening
  $("welcomeNotice").hidden = !s.notice;
  $("welcomeNotice").textContent = s.notice || "";
  $("dumpName").textContent = s.loaded ? `${s.dump} · ${s.dumpMB} MB` : "No flash dump";

  const run = $("runBtn");
  const showRun = s.paused || !s.loaded;
  run.querySelector("use").setAttribute("href", showRun ? "#i-play" : "#i-pause");
  run.querySelector("span").textContent = showRun ? "Run" : "Pause";
  run.classList.toggle("primary", s.paused || !s.loaded);
  for (const id of ["runBtn", "saveBtn", "loadBtn", "shotBtn", "installBtn"]) $(id).disabled = !s.loaded;
  document.querySelectorAll("[data-dbg]").forEach((b) => { b.disabled = !canStep(); });
  document.querySelectorAll("#speedSeg button").forEach((b) => b.classList.toggle("on", b.dataset.speed === s.speed));

  let state = s.loading ? "Opening the dump…" : "No dump", cls = s.loading ? "booting" : "";
  if (s.loaded) {
    if (s.fault) { state = "Stopped"; cls = "fault"; }
    else if (s.installing) { state = "Installing…"; cls = "booting"; }
    else if (s.paused && s.stop && s.stop.reason !== "pause") { state = "Stopped"; cls = "paused"; }
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
  const stop = s.loaded && s.paused && !s.fault && s.stop ? s.stop.text : "";
  $("stopText").textContent = stop;
  $("stopText").title = stop;
  const a = s.analysis || {};
  $("anaText").textContent = s.loaded && a.state === "running" ? `Analysing the OS: ${a.phase}${a.total ? " " + Math.round((100 * a.done) / a.total) + "%" : "…"}` : "";
}

let lastFault = "";
function onStatus(s) {
  status = s;
  if (s.fault && s.fault !== lastFault) log("error", "The calculator stopped: " + s.fault);
  lastFault = s.fault || "";
  render();
  engine.status(s);
}

// The first stop of a session while only the calculator is open: offer the debugger's layout.
function offerDebugging(st) {
  if (!workspace.onlyCalculator()) return;
  const t = document.createElement("div");
  t.className = "toast offer";
  t.innerHTML = `<div><b>${st.text.replace(/</g, "&lt;")}</b><br>Show the debugger's windows?</div>`;
  const yes = document.createElement("button");
  yes.className = "btn primary";
  yes.textContent = "Debugging layout";
  yes.onclick = () => { t.remove(); workspace.usePreset("debugging"); };
  const no = document.createElement("button");
  no.className = "btn";
  no.textContent = "Not now";
  no.onclick = () => t.remove();
  const row = document.createElement("div");
  row.className = "offer-actions";
  row.append(yes, no);
  t.append(row);
  $("toasts").append(t);
  setTimeout(() => t.remove(), 15000);
}

// ---- start ---------------------------------------------------------------------------
// The workspace waits (briefly) for the Go bindings, so the saved layout comes from Go;
// in a plain browser without them it starts from localStorage.
function whenGo(ms) {
  return new Promise((res) => {
    const t0 = Date.now();
    (function wait() { if (window.go?.main?.App && window.runtime) res(true); else if (Date.now() - t0 > ms) res(false); else setTimeout(wait, 30); })();
  });
}

async function boot() {
  const ok = await whenGo(3000);
  await workspace.init($("dock"));
  renderKeysHint(workspace.keyOwner());
  if (!ok) { log("warn", "Not running inside CG50 Studio: the Go side is missing"); return; }
  engine.init({ firstStop: offerDebugging });
  window.runtime.EventsOn("status", onStatus);
  window.runtime.EventsOn("toast", (t) => toast(t.kind, t.text));
  go().GetStatus().then(onStatus);
  go().Version().then((v) => { $("version").textContent = "v" + v; $("helpVersion").textContent = "CG50 Studio " + v; log("info", "CG50 Studio " + v); });
}
render();
boot();

// for the console of the web view, and for wiring the debug engine in
window.studio = { workspace, nav, debug, breakpoints, engine };
