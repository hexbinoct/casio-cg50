// The workspace: every window is a dock panel (dockview-core, vendor/dockview, MIT). Drag a
// tab to dock it beside or into another group (or onto the edge of the workspace), drag the
// splitters, float a group over the others, maximise it. The layout is saved through Go
// (layout.json in the app's settings folder; localStorage when Go isn't there) and comes
// back on the next start. Presets: Calculator (the app as it was), Debugging, Reverse
// engineering.
//
// A window kind is a module in panels/ with
//   meta = { kind, title, multi }   multi: several may be open (Memory 1, Memory 2, …)
//   create(ctx) → { element, focus?, keydown?(e) → handled, keyup?, onShow?, onHide?, dispose? }
//   ctx = { id, params (the window's saved view state), save(partialState) }
import { createDockview } from "../vendor/dockview/dockview-core.js";
import * as calculator from "./panels/calculator.js";
import * as disasm from "./panels/disasm.js";
import * as memory from "./panels/memory.js";
import * as registers from "./panels/registers.js";
import * as breakpoints from "./panels/breakpoints.js";
import * as callstack from "./panels/callstack.js";
import * as strings from "./panels/strings.js";
import * as syscalls from "./panels/syscalls.js";
import * as trace from "./panels/trace.js";
import * as consoleWin from "./panels/console.js";
import { h, icon } from "./panels/pane.js";
import { nav } from "./nav.js";

const KINDS = Object.fromEntries([calculator, disasm, memory, registers, breakpoints, callstack, strings, syscalls, trace, consoleWin].map((m) => [m.meta.kind, m]));

// the Windows menu, in this order, with a gap between the groups
const MENU = [["calculator"], ["disasm", "memory", "registers"], ["breakpoints", "callstack", "trace"], ["strings", "syscalls"], ["console"]];

const THEME = {
  name: "studio",
  className: "dockview-theme-studio",
  colorScheme: "dark",
  gap: 6,
  dndOverlayMounting: "absolute",
  dndPanelOverlay: "group",
  dndTabIndicator: "line",
};

let api = null;
let host = null;
const live = new Map(); // panel id → { kind, inst, api }
const ownerListeners = new Set();

// ---- windows ----------------------------------------------------------------------------

function renderer(kind, id) {
  const element = h("div.dv-host");
  let inst = null;
  return {
    element,
    init(p) {
      const mod = KINDS[kind];
      const params = { ...(p.params || {}) };
      const ctx = {
        id,
        params,
        save(st) { Object.assign(params, st); p.api.updateParameters({ ...params }); queueSave(); },
      };
      inst = mod ? mod.create(ctx) : { element: h("div.pane", {}, h("div.empty", { text: `Unknown window "${kind}"` })) };
      element.append(inst.element);
      live.set(id, { kind, inst, api: p.api });
      p.api.onDidVisibilityChange((e) => (e.isVisible ? inst.onShow?.() : inst.onHide?.()));
    },
    focus() { inst?.focus?.(); },
    dispose() { inst?.dispose?.(); live.delete(id); },
  };
}

function titleFor(kind, n) {
  const t = KINDS[kind].meta.title;
  return n > 1 || kind === "memory" ? `${t} ${n}` : t;
}

function nextId(kind) {
  if (!KINDS[kind].meta.multi) return [kind, 1];
  for (let n = 1; ; n++) if (!api.getPanel(`${kind}-${n}`)) return [`${kind}-${n}`, n];
}

function panelsOf(kind) { return api.panels.filter((p) => live.get(p.id)?.kind === kind || p.id === kind || p.id.startsWith(kind + "-")); }

// where a new window of a kind goes: next to its relatives if they are open
const PLACE = {
  calculator: [[null, "left"]],
  disasm: [["disasm", "within"], ["calculator", "right"]],
  memory: [["memory", "within"], ["disasm", "below"], ["calculator", "below"]],
  registers: [["callstack", "above"], ["disasm", "right"], ["calculator", "right"]],
  breakpoints: [["callstack", "within"], ["registers", "below"], ["disasm", "right"]],
  callstack: [["breakpoints", "within"], ["registers", "below"], ["disasm", "right"]],
  trace: [["memory", "within"], ["console", "within"], ["disasm", "below"]],
  strings: [["syscalls", "within"], ["disasm", "left"]],
  syscalls: [["strings", "within"], ["disasm", "left"]],
  console: [["trace", "within"], ["memory", "within"], ["calculator", "below"]],
};

// open adds a window of a kind (or focuses the open one, unless fresh: a new instance).
export function open(kind, { fresh = false, position, size, params } = {}) {
  const have = panelsOf(kind);
  if (have.length && (!fresh || !KINDS[kind].meta.multi)) {
    const p = have[have.length - 1];
    p.api.setActive();
    live.get(p.id)?.inst.focus?.();
    return p;
  }
  const [id, n] = nextId(kind);
  const opts = { id, component: kind, title: titleFor(kind, n), params: params || {} };
  if (position) opts.position = position;
  else {
    for (const [ref, dir] of PLACE[kind] || []) {
      if (ref === null) { opts.position = { direction: dir }; break; }
      const r = panelsOf(ref).pop();
      if (r && r.api.location.type === "grid") { opts.position = { referencePanel: r.id, direction: dir }; break; }
    }
    if (!opts.position && api.panels.length) opts.floating = { width: 520, height: 340 };
  }
  if (size?.width) opts.initialWidth = size.width;
  if (size?.height) opts.initialHeight = size.height;
  const p = api.addPanel(opts);
  if (!building) requestAnimationFrame(() => live.get(id)?.inst.focus?.());
  return p;
}

// ---- presets ----------------------------------------------------------------------------

const PRESETS = {
  calculator: { title: "Calculator", build() { open("calculator"); } },
  debugging: {
    title: "Debugging",
    build() {
      const W = host.clientWidth, H = host.clientHeight;
      add("calculator");
      add("disasm", { referencePanel: "calculator", direction: "right" });
      add("registers", { referencePanel: "disasm-1", direction: "right" });
      add("memory", { referencePanel: "disasm-1", direction: "below" });
      add("trace", { referencePanel: "memory-1", direction: "within" });
      add("console", { referencePanel: "calculator", direction: "below" });
      add("callstack", { referencePanel: "registers", direction: "below" });
      add("breakpoints", { referencePanel: "callstack", direction: "within" });
      size("calculator", { width: Math.round(W * 0.31), height: Math.round(H * 0.56) });
      size("registers", { width: Math.round(W * 0.22), height: Math.round(H * 0.52) });
      size("memory-1", { height: Math.round(H * 0.40) });
      api.getPanel("memory-1").api.setActive();
      api.getPanel("callstack").api.setActive();
    },
  },
  re: {
    title: "Reverse engineering",
    build() {
      const W = host.clientWidth, H = host.clientHeight;
      add("disasm");
      add("syscalls", { referencePanel: "disasm-1", direction: "left" });
      add("strings", { referencePanel: "syscalls", direction: "within" });
      add("calculator", { referencePanel: "disasm-1", direction: "right" });
      add("memory", { referencePanel: "disasm-1", direction: "below" });
      add("console", { referencePanel: "memory-1", direction: "within" });
      add("memory", { referencePanel: "calculator", direction: "below" }, true);
      size("syscalls", { width: Math.round(W * 0.2) });
      size("calculator", { width: Math.round(W * 0.3), height: Math.round(H * 0.45) });
      size("memory-1", { height: Math.round(H * 0.34) });
      api.getPanel("syscalls").api.setActive();
      api.getPanel("memory-1").api.setActive();
    },
  },
};

function add(kind, position, fresh = false) { return open(kind, { position, fresh }); }
function size(id, s) { api.getPanel(id)?.api.group.api.setSize(s); }

export const presets = Object.entries(PRESETS).map(([id, p]) => ({ id, title: p.title }));
export const DEFAULT_PRESET = "calculator";

let building = false;
export function usePreset(id) {
  const p = PRESETS[id];
  if (!p) return false;
  building = true;
  try { api.clear(); p.build(); } finally { building = false; }
  solo();
  queueSave();
  focusMain();
  return true;
}

// focusMain gives the keyboard to the calculator (or the first window) after a layout change.
function focusMain() {
  lastUsed = live.has("calculator") ? "calculator" : [...live.keys()][0] ?? null;
  requestAnimationFrame(() => { live.get(lastUsed)?.inst.focus?.(); ownerMaybeChanged(); });
}

export function resetLayout() { usePreset(DEFAULT_PRESET); }

// ---- the calculator alone looks like the app did: no tab bar, no frame ------------------
function solo() {
  const only = api.panels.length === 1 && api.panels[0].id === "calculator" && api.panels[0].api.location.type === "grid";
  host.classList.toggle("solo", only);
  for (const g of api.groups) {
    const hide = only && g.panels.length === 1;
    if (g.model?.header && g.model.header.hidden !== hide) g.model.header.hidden = hide;
  }
}

// ---- saving -----------------------------------------------------------------------------
const STORE_KEY = "cg50studio.layout";
let saveT = 0;
let restoring = false;

function queueSave() {
  if (restoring || !api) return;
  clearTimeout(saveT);
  saveT = setTimeout(save, 500);
}

function save() {
  const doc = JSON.stringify({ v: 1, dock: api.toJSON() });
  try { localStorage.setItem(STORE_KEY, doc); } catch (_) { /* private mode */ }
  window.go?.main?.App?.SaveLayout?.(doc).catch(() => {});
}

async function loadSaved() {
  let text = "";
  const goApp = window.go?.main?.App;
  if (goApp?.LoadLayout) { try { text = await goApp.LoadLayout(); } catch (_) { text = ""; } }
  if (!text) { try { text = localStorage.getItem(STORE_KEY) || ""; } catch (_) { text = ""; } }
  if (!text) return null;
  try {
    const doc = JSON.parse(text);
    return doc?.v === 1 && doc.dock ? doc : null;
  } catch (_) { return null; }
}

// forgetSaved drops the saved layout (Reset layout): the next start uses the default.
function forgetSaved() {
  try { localStorage.removeItem(STORE_KEY); } catch (_) {}
  window.go?.main?.App?.SaveLayout?.("").catch(() => {});
}

// ---- group header buttons: float / dock, maximise / restore ----------------------------
function headerActions(group) {
  const el = h("div.dv-actions");
  const floatBtn = h("button.hbtn");
  const maxBtn = h("button.hbtn");
  el.append(floatBtn, maxBtn);
  const sync = () => {
    const floating = group.api.location.type === "floating";
    floatBtn.replaceChildren(icon(floating ? "dock" : "float", 14));
    floatBtn.title = floating ? "Dock back into the workspace" : "Float above the other windows";
    floatBtn.hidden = !floating && api.groups.filter((g) => g.api.location.type === "grid").length < 2;
    const max = group.api.isMaximized();
    maxBtn.replaceChildren(icon(max ? "restore" : "maximize", 14));
    maxBtn.title = max ? "Restore the layout" : "Maximise (the other windows step aside)";
    maxBtn.hidden = floating || (!max && api.groups.length < 2);
  };
  floatBtn.onclick = (e) => {
    e.stopPropagation();
    if (group.api.location.type === "floating") dockBack(group);
    else {
      // a little off where it was, the same size up to 640 × 480, inside the workspace
      const b = group.element.getBoundingClientRect(), r = host.getBoundingClientRect();
      const width = Math.min(r.width - 40, Math.max(360, Math.min(b.width, 640)));
      const height = Math.min(r.height - 40, Math.max(260, Math.min(b.height, 480)));
      const x = Math.max(12, Math.min(b.left - r.left + 24, r.width - width - 18));
      const y = Math.max(12, Math.min(b.top - r.top + 24, r.height - height - 18));
      api.addFloatingGroup(group, { x, y, width, height });
    }
  };
  maxBtn.onclick = (e) => {
    e.stopPropagation();
    if (group.api.isMaximized()) group.api.exitMaximized(); else group.api.maximize();
  };
  const subs = [
    group.api.onDidLocationChange(sync),
    api.onDidMaximizedGroupChange(sync),
    api.onDidAddGroup(sync),
    api.onDidRemoveGroup(sync),
  ];
  return {
    element: el,
    init: sync,
    dispose: () => subs.forEach((s) => s.dispose()),
  };
}

// dockBack puts a floating group's windows back into the grid, beside the biggest group.
function dockBack(group) {
  const grid = api.groups.filter((g) => g.api.location.type === "grid");
  const target = grid.sort((a, b) => b.api.width * b.api.height - a.api.width * a.api.height)[0];
  const panels = group.panels.slice();
  if (!target || !panels.length) return;
  const first = panels[0];
  first.api.moveTo({ group: target, position: "right" });
  for (const p of panels.slice(1)) p.api.moveTo({ group: first.api.group, position: "center" });
}

function watermark() {
  const el = h("div.watermark", {},
    h("div.wm-card", {},
      h("h3", { text: "No windows open" }),
      h("p", { text: "Open one from the Windows menu, or start from a layout:" }),
      h("div.wm-actions", {}, ...presets.map((p) => h("button.btn", { text: p.title, onclick: () => usePreset(p.id) })))));
  return { element: el, init() {} };
}

// ---- keyboard owner: the window whose element has the focus, else the active one ---------
// Focus in the top bar (a button just clicked) or nowhere keeps the last window used.
let lastUsed = null;
export function keyOwner() {
  const a = document.activeElement;
  if (a && a !== document.body) {
    for (const [id, w] of live) if (w.inst.element.contains(a)) { lastUsed = id; return { id, ...w }; }
  }
  if (lastUsed && live.has(lastUsed)) return { id: lastUsed, ...live.get(lastUsed) };
  const p = api?.activePanel;
  return p && live.has(p.id) ? { id: p.id, ...live.get(p.id) } : null;
}
export function onOwnerChange(fn) { ownerListeners.add(fn); }
let lastOwner = null;
function ownerMaybeChanged() {
  const o = keyOwner();
  const id = o?.id ?? null;
  if (id === lastOwner) return;
  const was = lastOwner;
  lastOwner = id;
  for (const fn of ownerListeners) fn(o, was);
}

// ---- the Windows menu -------------------------------------------------------------------
export function fillMenu(list, close) {
  list.textContent = "";
  const item = (label, on, fn, extra = "") => {
    const b = h("button" + (on ? ".on" : ""), { onclick: () => { close(); fn(); } });
    const t = icon("check", 14);
    t.classList.add("tick");
    b.append(t, h("span.grow", { text: label }));
    if (extra) b.append(h("kbd", { text: extra }));
    return b;
  };
  list.append(h("h6", { text: "Windows" }));
  MENU.forEach((group, gi) => {
    if (gi) list.append(h("hr"));
    for (const kind of group) {
      const m = KINDS[kind].meta;
      const n = panelsOf(kind).length;
      list.append(item(m.title, n > 0, () => open(kind), m.multi && n > 1 ? `${n} open` : ""));
      if (m.multi && n > 0) list.append(item(`New ${m.title.toLowerCase()} view`, false, () => open(kind, { fresh: true })));
    }
  });
  list.append(h("hr"), h("h6", { text: "Layouts" }));
  for (const p of presets) list.append(item(p.title, false, () => usePreset(p.id)));
  list.append(h("hr"), item("Reset layout", false, () => { forgetSaved(); resetLayout(); }));
}

// ---- start ------------------------------------------------------------------------------
export async function init(el) {
  host = el;
  api = createDockview(el, {
    theme: THEME,
    dndStrategy: "pointer", // pointer events: no clash with the window's file drop (Wails)
    floatingGroupDragHandle: "tabbar",
    createComponent: ({ id, name }) => renderer(name, id),
    createRightHeaderActionComponent: headerActions,
    createWatermarkComponent: watermark,
  });
  api.onDidLayoutChange(() => { solo(); queueSave(); });
  api.onDidActivePanelChange((p) => { if (p && !building) lastUsed = p.id; ownerMaybeChanged(); });
  document.addEventListener("focusin", ownerMaybeChanged);

  nav.open = (kind) => open(kind);
  nav.code = (addr) => { open("disasm"); disasm.goTo(addr); };
  nav.data = (addr) => { open("memory"); memory.goTo(addr); };
  nav.layout = (name) => usePreset(name === "re" || name === "reverse" ? "re" : name);

  const doc = await loadSaved();
  restoring = true;
  let ok = false;
  if (doc) {
    try { api.fromJSON(doc.dock); ok = api.panels.length > 0; } catch (err) { console.warn("layout not restored:", err); }
  }
  if (!ok) { building = true; api.clear(); PRESETS[DEFAULT_PRESET].build(); building = false; }
  restoring = false;
  solo();
  focusMain();
  return api;
}

export const dock = () => api;

// onlyCalculator: the workspace shows the calculator and nothing else (the default layout).
export function onlyCalculator() { return !!api && api.panels.length === 1 && api.panels[0].id === "calculator"; }
