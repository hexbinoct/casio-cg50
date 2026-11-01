// The debug engine on the page: connects every window to the running machine in Go
// (window.go.main.App, app/debug.go and app/analysis.go).
//
//   - when a dump is open: the regions, the listings (the disassembly's address spaces, which
//     grow names when the OS's analysis is done), the syscall table, the string search, the
//     breakpoints saved for that dump; again when the dump changes or the analysis moves on
//   - at every stop ("debugstop" event, or a step's own result): one DebugSnapshot gives the
//     registers, the call stack and the hit counts; the PC marker moves, memory views and RAM
//     listings read again, the trace is fetched if it records, the console says why
//   - the debugger's actions (debugger.js backend) and the console's debugger commands
//
// app.js calls init(hooks) once the Go side is there and status(s) with each status.
import * as dbg from "./debugger.js";
import * as disasm from "./panels/disasm.js";
import * as memory from "./panels/memory.js";
import * as registers from "./panels/registers.js";
import * as callstack from "./panels/callstack.js";
import * as trace from "./panels/trace.js";
import * as strings from "./panels/strings.js";
import * as syscalls from "./panels/syscalls.js";
import { addCommand, log } from "./panels/console.js";
import { hex, parseAddr } from "./vlist.js";
import { nav } from "./nav.js";

const go = () => window.go.main.App;

let hooks = { stop: () => {}, firstStop: () => {} };
let dumpHash = "";
let gen = -1;
let analysisState = "";
let appliedSeq = 0;
let lastSnap = null;
let wasPaused = null;
let firstStopSeen = false;
let memTimer = 0;

// ---- sources the windows read through ----------------------------------------------------

const listingSource = {
  rows: (space, first, n) => go().DisasmRows(space, first, n),
  rowOf: (space, addr) => go().DisasmRowOf(space, addr >>> 0),
  lookup: (name) => go().Lookup(name),
  xrefs: (addr) => go().Xrefs(addr >>> 0),
};

function b64(s) {
  if (!s) return new Uint8Array(0);
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

async function readMem(addr, len) {
  const b = await go().ReadMemory(addr >>> 0, len);
  return { bytes: b64(b.data), err: b.err || "" };
}

function memorySource(regions) {
  return {
    regions,
    read: readMem,
    write: async (addr, bytes) => {
      await go().WriteMemory(addr >>> 0, Array.from(bytes));
      disasm.invalidate();
    },
  };
}

const stringsProvider = (regions) => ({
  regions,
  async search(region, q, { encoding, minLength }) {
    const r = await go().SearchStrings(region, q, encoding, minLength);
    return { count: r.count, total: r.total, fetch: (first, n) => go().StringRows(r.token, first, n) };
  },
});

// ---- the debugger's backend ----------------------------------------------------------------

async function stepped(p, what) {
  const r = await p;
  if (r?.stopped && r.snap) apply(r.snap);
  else log("info", `${what}: running until it ends (a function waiting for a key needs one)…`);
}

const backend = {
  stepInto: () => stepped(go().StepInto(), "Step into"),
  stepOver: () => stepped(go().StepOver(), "Step over"),
  stepOut: () => stepped(go().StepOut(), "Step out"),
  runToCursor: (addr) => stepped(go().RunTo(addr >>> 0), "Run to " + hex(addr)),
  setBreakpoints: (list) => go().SetBreakpoints(list),
};

// ---- loading -------------------------------------------------------------------------------

let loadingListings = false;
async function loadListings() {
  if (loadingListings) return;
  loadingListings = true;
  try {
    const l = await go().DisasmSpaces();
    gen = l.gen;
    disasm.setListings(l, listingSource);
    const regs = await go().MemoryRegions();
    memory.setMemory(memorySource(regs));
  } finally { loadingListings = false; }
}

async function loadSyscalls() {
  syscalls.setSyscalls(await go().Syscalls());
}

async function dumpOpened() {
  dbg.setBackend(backend);
  dbg.restoreBreakpoints(await go().GetBreakpoints());
  await loadListings();
  await loadSyscalls();
  strings.setStrings(stringsProvider(await go().StringRegions()));
  go().SetTraceDepth(trace.recording() ? 65536 : 0);
}

// ---- stops -----------------------------------------------------------------------------------

function groups(sn) {
  const r = sn.regs;
  const g = (name, names, extra = {}) => ({ name, regs: names.map((n) => ({ name: n, value: r[n] ?? 0, ...(extra[n] || {}) })) });
  const other = 1 - sn.bank;
  return [
    g("General", Array.from({ length: 16 }, (_, i) => "r" + i)),
    g("Control", ["pc", "pr", "sr", "gbr", "vbr", "ssr", "spc", "sgr", "dbr"]),
    g("Multiply", ["mach", "macl"]),
    g(`Bank ${other} (the other r0–r7)`, Array.from({ length: 8 }, (_, i) => `r${i}_bank${other}`)),
    g("DSP words", ["fpul", "fpscr"], { fpscr: { title: "The words lds/sts move with the FPSCR encoding: DSR on the SH4AL-DSP" }, fpul: { title: "The words lds/sts move with the FPUL encoding" } }),
  ];
}

// apply shows a snapshot in every window.
function apply(sn) {
  lastSnap = sn;
  const st = sn.stop;
  registers.setRegisters(groups(sn));
  callstack.setCallStack(sn.frames || []);
  dbg.applyInfo(sn.breakpoints);
  disasm.invalidate();
  disasm.setPC(sn.regs.pc);
  memory.refresh();
  if (trace.recording()) go().DebugTrace(4096).then((t) => trace.setTrace(t.entries, { total: t.total })).catch(() => {});
  if (st && st.seq > appliedSeq) {
    appliedSeq = st.seq;
    log(st.reason === "fault" ? "error" : st.reason === "step" ? "out" : "warn", st.text);
    hooks.stop(st);
    if (!firstStopSeen && st.reason !== "pause" && st.reason !== "step") {
      firstStopSeen = true;
      hooks.firstStop(st);
    }
  }
}

let fetchingSeq = 0;
async function onStopEvent(st) {
  if (!st || st.seq <= appliedSeq || st.seq <= fetchingSeq) return;
  fetchingSeq = st.seq;
  try { apply(await go().DebugSnapshot()); } catch (err) { log("error", "snapshot: " + err); }
}

// running again: the stop windows keep their values, marked as from the last stop
function onRunning() {
  disasm.setPC(null);
  registers.setRegisters(null, { stale: true });
  callstack.setCallStack(null, { stale: true });
}

// ---- entry points (app.js) -------------------------------------------------------------------

export function init(h) {
  hooks = { ...hooks, ...h };
  window.runtime.EventsOn("debugstop", onStopEvent);
  window.runtime.EventsOn("listing", (a) => { if (a && a.gen !== gen && dumpHash) { loadListings(); if (a.state === "ready") loadSyscalls(); } });
  registers.setEditor(async (name, value) => apply(await go().SetRegister(name, value >>> 0)));
  trace.onRecord((on) => { if (dumpHash) go().SetTraceDepth(on ? 65536 : 0).catch(() => {}); });
  memTimer = setInterval(() => { if (dumpHash && wasPaused === false) memory.refresh(); }, 1000);
}

export function status(s) {
  if (!s.loaded) return;
  if (s.dumpHash !== dumpHash) {
    dumpHash = s.dumpHash;
    gen = -1;
    appliedSeq = 0;
    dumpOpened().catch((err) => log("error", "Debugger: " + err));
  } else if (s.analysis && s.analysis.gen !== gen) {
    loadListings().catch(() => {});
  }
  if (s.analysis?.state !== analysisState) {
    if (s.analysis?.state === "ready") {
      loadSyscalls();
      const a = s.analysis;
      log("info", `OS analysed: ${a.funcs.toLocaleString()} functions in ${a.ms} ms${a.cached ? " (function table from the cache)" : ""}`);
    }
    analysisState = s.analysis?.state;
  }
  if (s.paused && s.stop && s.stop.seq > appliedSeq) onStopEvent(s.stop);
  if (wasPaused === true && !s.paused) onRunning();
  wasPaused = s.paused;
}

export function snapshot() { return lastSnap; }

// ---- console commands ------------------------------------------------------------------------

const need = (print) => { if (!dumpHash) { print("open a flash dump first"); return false; } return true; };
const addrArg = async (t) => (t ? disasm.findName(t) : null);

addCommand("c", "continue (run from a stop)", () => go().SetPaused(false));
addCommand("pause", "stop the machine where it is", () => go().SetPaused(true));
addCommand("s", "step into (one instruction)", () => dbg.actions.stepInto());
addCommand("n", "step over (calls run through)", () => dbg.actions.stepOver());
addCommand("finish", "step out (run until this function returns)", () => dbg.actions.stepOut());
addCommand("until", "<address|name>  run until the PC gets there", async ([a], print) => {
  const x = await addrArg(a);
  if (x == null) print("until: an address or a function's name"); else backend.runToCursor(x);
});
addCommand("r", "the registers at the last stop", (_, print) => {
  const sn = lastSnap;
  if (!sn) { print("no stop yet"); return; }
  const r = sn.regs;
  for (let i = 0; i < 16; i += 4) print([0, 1, 2, 3].map((k) => `r${i + k}`.padStart(3) + " " + hex(r["r" + (i + k)])).join("   "));
  print(["pc", "pr", "sr", "gbr"].map((n) => n.padStart(3) + " " + hex(r[n])).join("   "));
  print(["vbr", "spc", "ssr", "macl"].map((n) => n.padStart(4) + " " + hex(r[n])).join("  "));
});
addCommand("set", "<register> <value>  change a register (hex), e.g. set r4 8C001000", async ([n, v], print) => {
  const x = parseAddr(v || "");
  if (!n || x == null) { print("set: a register and a hex value"); return; }
  apply(await go().SetRegister(n, x));
  print(`${n} = ${hex(x)}`);
});
addCommand("x", "<address|name> [bytes]  hex dump (default 64)", async ([a, n], print) => {
  if (!need(print)) return;
  const x = await addrArg(a);
  if (x == null) { print("x: an address"); return; }
  const len = Math.min(4096, Math.max(1, Number(n) || 64));
  const b = await go().ReadMemory(x, len);
  const bytes = b64(b.data);
  for (let o = 0; o < bytes.length; o += 16) {
    const row = Array.from(bytes.slice(o, o + 16));
    print(`${hex(x + o)}  ${row.map((v) => v.toString(16).padStart(2, "0")).join(" ").padEnd(47)}  ${row.map((v) => (v >= 32 && v < 127 ? String.fromCharCode(v) : ".")).join("")}`);
  }
  if (bytes.length < len) print(`${hex(x + bytes.length)}: ${b.err}`);
});
addCommand("w", "<address> <byte> …  write bytes (hex)", async ([a, ...bs], print) => {
  const x = parseAddr(a || "");
  const v = bs.map((t) => parseInt(t, 16));
  if (x == null || !v.length || v.some((k) => !(k >= 0 && k < 256))) { print("w: an address and hex bytes, e.g. w 8C001000 12 34"); return; }
  const n = await go().WriteMemory(x, v);
  print(`wrote ${n} byte${n === 1 ? "" : "s"} at ${hex(x)}`);
  memory.refresh(); disasm.invalidate();
});
addCommand("bt", "the call stack at the last stop", (_, print) => {
  const fr = lastSnap?.frames || [];
  if (!fr.length) { print("no stop yet"); return; }
  fr.forEach((f, i) => print(`#${i} ${hex(f.addr)}  ${f.name || "?"}${i && !f.confirmed ? "  (unchecked)" : ""}`));
});
addCommand("bs", "<number|name|*>  break when an add-in calls a syscall", ([t], print) => {
  if (!t) { print("bs: a syscall number (hex), a name, or * for all"); return; }
  const any = t === "*";
  const num = any ? 0 : parseAddr(t) ?? syscalls.syscallNumber(t);
  if (num == null) { print(`bs: no syscall called ${t}`); return; }
  const b = dbg.addBreakpoint(num, "syscall", { any });
  print(`breakpoint ${b.id}: ${any ? "every syscall" : "syscall " + hex(num, 4) + " " + syscalls.syscallName(num)}`);
});
addCommand("be", "<code|*>  break on an exception (* = all but TLB misses)", ([t], print) => {
  const any = t === "*", code = any ? 0 : parseAddr(t || "");
  if (code == null) { print("be: an EXPEVT code (180 illegal instruction, 160 trapa, …) or *"); return; }
  const b = dbg.addBreakpoint(code, "exception", { any });
  print(`breakpoint ${b.id}: exception ${any ? "(all but TLB misses)" : hex(code, 3)}`);
});
addCommand("bi", "<code|*>  break on an interrupt (BE0 key scan, 560 OS tick, …)", ([t], print) => {
  const any = t === "*", code = any ? 0 : parseAddr(t || "");
  if (code == null) { print("bi: an INTEVT code or *"); return; }
  const b = dbg.addBreakpoint(code, "interrupt", { any });
  print(`breakpoint ${b.id}: interrupt ${any ? "(all)" : hex(code, 3)}`);
});
addCommand("bl", "list the breakpoints", (_, print) => {
  const l = dbg.breakpoints();
  if (!l.length) print("no breakpoints");
  for (const b of l) print(`${String(b.id).padStart(3)} ${b.enabled ? "on " : "off"} ${b.kind.padEnd(9)} ${b.any ? "all" : hex(b.addr, b.kind === "syscall" ? 4 : 8)}  hits ${b.hits || 0}  ${b.where || ""}${b.condition ? "  if " + b.condition : ""}`);
});
addCommand("bd", "<id>  remove a breakpoint", ([t], print) => {
  const id = Number(t);
  if (!dbg.breakpoints().some((b) => b.id === id)) { print("bd: a breakpoint id (bl lists them)"); return; }
  dbg.removeBreakpoint(id);
});
addCommand("xref", "<address|name>  who refers to it", async ([a], print) => {
  const x = await addrArg(a);
  if (x == null) { print("xref: an address or a function's name"); return; }
  const refs = await go().Xrefs(x);
  print(`${refs.length} reference${refs.length === 1 ? "" : "s"} to ${hex(x)}`);
  for (const r of refs.slice(0, 200)) print(`  ${hex(r.from)}  ${r.kind.padEnd(6)} ${(r.fn || "").padEnd(28)} ${r.text}`);
  if (refs.length > 200) print(`  … ${refs.length - 200} more`);
});
addCommand("info", "<address|name>  what an address is (region, bus address, function)", async ([a], print) => {
  const x = await addrArg(a);
  if (x == null) { print("info: an address or a function's name"); return; }
  print(await go().Describe(x));
});
addCommand("trace", "[n]  the last n instructions executed (needs the Trace window recording)", async ([n], print) => {
  const t = await go().DebugTrace(Math.min(1000, Number(n) || 20));
  if (!t.entries.length) { print("nothing recorded: open the Trace window (Record on), then stop the machine"); return; }
  for (const e of t.entries) print(`${hex(e.addr)}  ${e.text}`);
});
addCommand("strings", "<text>  search the text in memory (Strings window)", ([...w]) => { nav.open("strings"); strings.search(w.join(" ")); });
addCommand("sys", "<number|name>  a syscall's handler", ([t], print) => {
  const num = parseAddr(t || "") ?? syscalls.syscallNumber(t || "");
  const list = num == null ? [] : [num];
  if (!list.length) { print("sys: a number (hex) or a name"); return; }
  go().Syscalls().then((all) => {
    const s = all.find((x) => x.num === num);
    if (!s) print(`syscall ${hex(num, 4)}: not in the table`);
    else { print(`syscall ${hex(s.num, 4)} ${s.name || "(no name known)"} → ${hex(s.addr)}`); nav.code(s.addr); }
  });
});
