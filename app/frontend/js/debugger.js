// The debugger as the page sees it: the actions the toolbar, the shortcuts and the windows
// ask for, and the breakpoint list. The work is done by a backend (engine.js, on Go):
//
//   setBackend({
//     stepInto(), stepOver(), stepOut(), runToCursor(addr),   → Promise, the machine stops after
//     setBreakpoints(list) → Promise<infos>                   the whole list after every change;
//   })                                                         infos: hits, names, errors by id
//
// Breakpoint = { id, kind, addr, any, len, enabled, condition, ignore,      (what is set)
//                hits, where, note, error }                                 (what Go reports)
//   kind: "exec" | "read" | "write" | "access"    addr: the address (len: bytes watched)
//         "syscall"                                 addr: the number (any: every syscall)
//         "exception" | "interrupt"                 addr: EXPEVT / INTEVT (any: all)
import { nav } from "./nav.js";

let backend = null;

export function setBackend(b) { backend = b; }

// ---- run control ---------------------------------------------------------------------

let cursorFn = () => null; // the disassembly's selected address (panels/disasm.js sets it)
export function setCursorSource(fn) { cursorFn = fn; }

async function act(name, ...args) {
  if (!backend?.[name]) { nav.log("warn", `${name}: open a flash dump first`); return; }
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

let bps = [];
let nextId = 1;
const listeners = new Set();

export function onBreakpoints(fn) { listeners.add(fn); fn(bps); return () => listeners.delete(fn); }
export function breakpoints() { return bps; }
export function breakpointAt(addr) { return bps.find((b) => b.addr === addr && b.kind === "exec"); }
export function syscallBreak(num) { return bps.find((b) => b.kind === "syscall" && !b.any && b.addr === num); }

function notify() { for (const fn of listeners) fn(bps); }

let sendT = 0;
function changed() {
  bps = bps.slice();
  notify();
  clearTimeout(sendT);
  sendT = setTimeout(send, 30); // a burst of changes (clear all, restore) is one call
}

async function send() {
  if (!backend?.setBreakpoints) return;
  const list = bps.map(({ id, kind, addr, any, len, enabled, condition, ignore }) =>
    ({ id, kind, addr: addr >>> 0, any: !!any, len: len >>> 0, enabled, condition: condition || "", ignore: ignore || 0 }));
  try { applyInfo(await backend.setBreakpoints(list)); }
  catch (err) { nav.log("error", "Breakpoints: " + err); }
}

const same = (b, addr, kind, extra) => b.addr === addr && b.kind === kind && !!b.any === !!extra.any && (b.len || 0) === (extra.len || 0);

export function addBreakpoint(addr, kind = "exec", extra = {}) {
  addr = addr >>> 0;
  const old = bps.find((b) => same(b, addr, kind, extra));
  if (old) return old;
  const b = { id: nextId++, addr, kind, any: false, len: 0, enabled: true, hits: 0, condition: "", ignore: 0, where: "", ...extra };
  bps.push(b);
  changed();
  return b;
}

// toggleBreakpoint: none → on → removed (F9 in the disassembly).
export function toggleBreakpoint(addr) {
  const b = breakpointAt(addr >>> 0);
  if (b) removeBreakpoint(b.id);
  else addBreakpoint(addr);
}

export function setBreakpointEnabled(id, on) { updateBreakpoint(id, { enabled: on }); }

export function updateBreakpoint(id, patch) {
  const b = bps.find((x) => x.id === id);
  if (b) { Object.assign(b, patch); changed(); }
}

export function removeBreakpoint(id) { bps = bps.filter((b) => b.id !== id); changed(); }
export function clearBreakpoints() { bps = []; changed(); }

// applyInfo takes what Go reports (hit counts, names, errors) without sending anything back.
export function applyInfo(infos) {
  if (!infos) return;
  const by = new Map(infos.map((x) => [x.id, x]));
  let any = false;
  for (const b of bps) {
    const x = by.get(b.id);
    if (!x) continue;
    for (const k of ["hits", "where", "note", "error"]) if (b[k] !== (x[k] ?? (k === "hits" ? 0 : ""))) { b[k] = x[k] ?? (k === "hits" ? 0 : ""); any = true; }
  }
  if (any) { bps = bps.slice(); notify(); }
}

// restoreBreakpoints replaces the list with the one saved for this dump (ids kept).
export function restoreBreakpoints(list) {
  bps = (list || []).map((b) => ({ hits: 0, condition: "", ignore: 0, where: "", any: false, len: 0, ...b }));
  nextId = Math.max(0, ...bps.map((b) => b.id)) + 1;
  notify();
}
