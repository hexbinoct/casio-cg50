// The debugger as the page sees it: the actions the toolbar, the shortcuts and the windows
// ask for, and the breakpoint list. The work is done by a backend:
//
//   setBackend({
//     stepInto(), stepOver(), stepOut(), runToCursor(addr),   → Promise, the machine stops after
//     setBreakpoints(list),                                   the whole list, after every change
//   })
//
// Until the debug engine is connected, the backend is the SAMPLE session in sample.js: the
// steps move a made-up CPU state through the sample windows, so the wiring can be seen.
// Run / pause is real already (app.js, SetPaused).
import { nav } from "./nav.js";

let backend = null;

export function setBackend(b) { backend = b; backend?.setBreakpoints?.(bps.slice()); }

// ---- run control ---------------------------------------------------------------------

let cursorFn = () => null; // the disassembly's selected address (panels/disasm.js sets it)
export function setCursorSource(fn) { cursorFn = fn; }

async function act(name, ...args) {
  if (!backend?.[name]) { nav.log("warn", `${name}: no debugger connected`); return; }
  try { await backend[name](...args); }
  catch (err) { nav.log("error", `${name}: ${err}`); }
}

export const actions = {
  stepInto: () => act("stepInto"),
  stepOver: () => act("stepOver"),
  stepOut: () => act("stepOut"),
  runToCursor: () => {
    const a = cursorFn();
    if (a == null) { nav.log("warn", "Run to cursor: select a line in the disassembly first"); return; }
    return act("runToCursor", a);
  },
};

// ---- breakpoints ---------------------------------------------------------------------
// { id, addr, kind: "exec" | "read" | "write" | "access", enabled, hits, condition }

let bps = [];
let nextId = 1;
const listeners = new Set();

export function onBreakpoints(fn) { listeners.add(fn); fn(bps); return () => listeners.delete(fn); }
export function breakpoints() { return bps; }
export function breakpointAt(addr) { return bps.find((b) => b.addr === addr && b.kind === "exec"); }

function changed() {
  bps = bps.slice();
  for (const fn of listeners) fn(bps);
  backend?.setBreakpoints?.(bps.slice());
}

export function addBreakpoint(addr, kind = "exec", extra = {}) {
  const old = bps.find((b) => b.addr === addr && b.kind === kind);
  if (old) return old;
  const b = { id: nextId++, addr: addr >>> 0, kind, enabled: true, hits: 0, condition: "", ...extra };
  bps.push(b);
  changed();
  return b;
}

// toggleBreakpoint: none → on → removed (F9 in the disassembly).
export function toggleBreakpoint(addr) {
  const b = breakpointAt(addr);
  if (b) removeBreakpoint(b.id);
  else addBreakpoint(addr);
}

export function setBreakpointEnabled(id, on) {
  const b = bps.find((x) => x.id === id);
  if (b) { b.enabled = on; changed(); }
}

export function removeBreakpoint(id) { bps = bps.filter((b) => b.id !== id); changed(); }
export function clearBreakpoints() { bps = []; changed(); }

// setBreakpointHits: the engine reports hit counts (id → count).
export function setBreakpointHits(hits) {
  for (const b of bps) if (hits[b.id] !== undefined) b.hits = hits[b.id];
  changed();
}

// the list as saved with the layout's session (addresses, kinds, on/off; not hit counts)
export function breakpointsState() { return bps.map(({ addr, kind, enabled, condition }) => ({ addr, kind, enabled, condition })); }
export function restoreBreakpoints(list) {
  bps = [];
  for (const b of list || []) bps.push({ id: nextId++, hits: 0, condition: "", ...b });
  changed();
}
