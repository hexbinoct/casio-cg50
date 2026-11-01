// ============================================================================================
// SAMPLE DATA. Everything in this file is made up: a stand-in for the debug engine and the
// disassembler (being written in emu_go), so the windows can be built, laid out and tried.
// The instructions are well-formed SH-4 encodings at made-up places, the names and syscall
// numbers are examples, and the memory is generated. Each window shows a "sample" badge
// while its data comes from here.
//
// installSample() feeds every window through the same interface the engine will use
// (setDisassembly, setMemory, setRegisters, setCallStack, setTrace, setStrings, setSyscalls)
// and makes this the debugger backend (debugger.setBackend). Replacing it = calling those
// with real data instead.
// ============================================================================================
import { hex } from "./vlist.js";
import * as dbg from "./debugger.js";
import { setDisassembly, setPC } from "./panels/disasm.js";
import { setMemory, refresh as refreshMemory } from "./panels/memory.js";
import { setRegisters } from "./panels/registers.js";
import { setCallStack } from "./panels/callstack.js";
import { setTrace, pushTrace } from "./panels/trace.js";
import { setStrings } from "./panels/strings.js";
import { setSyscalls } from "./panels/syscalls.js";
import { nav } from "./nav.js";

const FLASH = 0x80000000, FLASH_SIZE = 32 << 20;
const BLOCK = 128;            // sample "functions" are 128 bytes: 64 instructions
const ROWS = 1 + BLOCK / 2;   // a name row + the instructions
const BLOCKS = FLASH_SIZE / BLOCK;
const STR_AT = 0x80B20000;    // where the sample strings live (shown as data in the listing)

function h32(x) {
  x = (x ^ (x >>> 16)) >>> 0;
  x = Math.imul(x, 0x7feb352d);
  x ^= x >>> 15;
  x = Math.imul(x, 0x846ca68b);
  x ^= x >>> 16;
  return x >>> 0;
}

// ---- syscalls (example names and numbers) ---------------------------------------------
const SYSCALL_NAMES = [
  [0x01E6, "GetVRAMAddress"], [0x025F, "Bdisp_PutDisp_DD"], [0x0272, "Bdisp_AllClr_VRAM"],
  [0x0263, "Bdisp_SetPoint_VRAM"], [0x0268, "Bdisp_GetPoint_VRAM"], [0x018A, "PrintXY"],
  [0x0239, "PrintCXY"], [0x023C, "PrintMini"], [0x0921, "Bdisp_EnableColor"],
  [0x0EAB, "GetKey"], [0x12BF, "GetKeyWait_OS"], [0x1DA3, "Bfile_OpenFile_OS"],
  [0x1DA4, "Bfile_CloseFile_OS"], [0x1DA6, "Bfile_GetFileSize_OS"], [0x1DAC, "Bfile_ReadFile_OS"],
  [0x1DAE, "Bfile_CreateEntry_OS"], [0x1DAF, "Bfile_WriteFile_OS"], [0x1DB4, "Bfile_DeleteEntry"],
  [0x1DB6, "Bfile_FindFirst"], [0x1DB8, "Bfile_FindNext"], [0x1DBA, "Bfile_FindClose"],
  [0x1F42, "sys_free"], [0x1F44, "sys_malloc"], [0x1E05, "OS_InnerWait_ms"],
  [0x0C4F, "MCS_SearchDirectoryItem"], [0x1A0A, "RTC_GetTicks"], [0x02A4, "Bdisp_MMPrint"],
  [0x0290, "Bdisp_Rectangle"], [0x0D39, "Keyboard_ClrBuffer"], [0x1E07, "Timer_Install"],
];
const SYSCALLS = [];
const NAMES = new Map(); // block address → name
for (const [num, name] of SYSCALL_NAMES) {
  const addr = FLASH + (h32(num * 2654435761) % BLOCKS) * BLOCK;
  SYSCALLS.push({ num, name, addr });
  NAMES.set(addr, name);
}
// numbers without a known name, to look like a real table
for (let n = 0; n < 0x2000; n += 7) {
  if (SYSCALLS.some((s) => s.num === n)) continue;
  SYSCALLS.push({ num: n, name: "", addr: FLASH + (h32(n + 77) % BLOCKS) * BLOCK });
}

const nameOf = (blockAddr) => NAMES.get(blockAddr) || "fn_" + hex(blockAddr);

// ---- strings (sample text placed at STR_AT) --------------------------------------------
const TEXTS = [
  "MAIN MENU", "Run-Matrix", "Statistics", "eActivity", "Spreadsheet", "Graph", "Dyna Graph",
  "Table", "Recursion", "Conic Graphs", "Equation", "Program", "Financial", "Link", "Memory",
  "System", "Python", "Distribution", "Vector", "Syntax ERROR", "Math ERROR", "Stack ERROR",
  "Memory ERROR", "Argument ERROR", "Dimension ERROR", "Go ERROR", "Nesting ERROR",
  "Press:[EXIT]", "Press:[AC]", "Complex Mode", "Angle", "Display", "Battery Settings",
  "Language", "Version", "Reset", "Storage Memory", "Main Memory", "SD Card", "Optimization",
  "Are you sure?", "Yes:[F1]", "No:[F6]", "Data Communication", "Receiving...", "Sending...",
  "Transfer complete", "Low battery", "Contrast", "Backlight Duration", "Power Properties",
  "@MainMem", "\\\\fls0\\", "PROGRAM", "Y=", "r=", "Xt=", "Yt=", "X=", "Ans", "SETUP",
  "Input/Output", "Math", "Line", "Frac Result", "d/c", "Simp", "Func Type", "Draw Type",
  "Connect", "Plot", "Derivative", "Background", "Sketch", "G-Solve", "Zoom", "V-Window",
  "Trace", "Cursor", "Coord", "Grid", "Axes", "Label", "Display", "Fix", "Sci", "Norm",
  "Eng", "Deg", "Rad", "Gra", "a+bi", "r∠θ", "√(x²+y²)", "Σ(x,x,1,10)", "∫(f(x),a,b)",
];
const STRINGS = [];
{
  let a = STR_AT;
  TEXTS.forEach((text, i) => {
    const encoding = /[^\x20-\x7e]/.test(text) ? "casio" : i % 23 === 7 ? "utf16" : "ascii";
    const len = encoding === "utf16" ? text.length * 2 + 2 : text.length + 1;
    STRINGS.push({ addr: a, text, encoding });
    a += (len + 3) & ~3;
  });
}
const STR_END = STRINGS[STRINGS.length - 1].addr + 64;
function strByte(addr) {
  for (const s of STRINGS) {
    const off = addr - s.addr;
    if (off < 0 || off > 72) continue;
    if (s.encoding === "utf16") {
      const c = off >> 1 < s.text.length ? s.text.charCodeAt(off >> 1) : 0;
      return off & 1 ? c & 0xff : c >> 8;
    }
    const bytes = new TextEncoder().encode(s.text);
    if (off < bytes.length) return bytes[off];
    if (off === bytes.length) return 0;
  }
  return 0;
}

// ---- instructions ------------------------------------------------------------------------
// insn(a) → a row of the listing; the encodings are real SH-4 ones (the memory view shows
// the same words), the choice of instruction is a hash of the address.
const FIXED = {
  0: ["4F22", "sts.l", "pr,@-r15"], 1: ["2FE6", "mov.l", "r14,@-r15"], 2: ["6EF3", "mov", "r15,r14"],
  59: ["6FE3", "mov", "r14,r15"], 60: ["6EF6", "mov.l", "@r15+,r14"], 61: ["4F26", "lds.l", "@r15+,pr"],
  62: ["000B", "rts", ""], 63: ["0009", "nop", "", "delay slot"],
};
const w4 = (v) => hex(v & 0xffff, 4);
const s8 = (v) => ((v & 0xff) ^ 0x80) - 0x80;

function insn(a) {
  if (a >= STR_AT && a < STR_END) {
    const hi = strByte(a), lo = strByte(a + 1);
    const ch = (c) => (c >= 0x20 && c < 0x7f ? String.fromCharCode(c) : ".");
    return { kind: "data", addr: a, bytes: w4((hi << 8) | lo), mnem: ".word", ops: "0x" + w4((hi << 8) | lo), comment: `"${ch(hi)}${ch(lo)}"`, word: (hi << 8) | lo };
  }
  const blockAddr = a - ((a - FLASH) % BLOCK);
  const k = (a - blockAddr) >> 1;
  const f = FIXED[k];
  if (f) return { kind: "insn", addr: a, bytes: f[0], mnem: f[1], ops: f[2], comment: f[3] ? "; " + f[3] : "", word: parseInt(f[0], 16) };
  const h = h32(a ^ 0x9e3779b9);
  const n = ((h >>> 4) & 15) % 14, m = ((h >>> 8) & 15) % 14;
  const r = (x) => "r" + x;
  const sel = h % 17;
  const mk = (word, mnem, ops, extra = {}) => ({ kind: "insn", addr: a, bytes: w4(word), mnem, ops, comment: "", word: word & 0xffff, ...extra });
  switch (sel) {
    case 0: case 1: case 2: {
      const d = (h >>> 12) & 0x3f;
      const lit = ((a + 4) & ~3) + d * 4;
      const val = (h32(lit) & 1 ? 0x8C000000 : 0x80000000) | (h32(lit + 1) & 0x1ffffc);
      return mk(0xD000 | (n << 8) | d, "mov.l", `@(0x${(d * 4).toString(16).toUpperCase()},pc),${r(n)}`, { comment: `; [${hex(lit)}] = ${hex(val)}` });
    }
    case 3: case 4: { const i = (h >>> 12) & 0xff; return mk(0xE000 | (n << 8) | i, "mov", `#${s8(i)},${r(n)}`); }
    case 5: { const i = (h >>> 12) & 0xff; return mk(0x7000 | (n << 8) | i, "add", `#${s8(i)},${r(n)}`); }
    case 6: case 7: return mk(0x6003 | (n << 8) | (m << 4), "mov", `${r(m)},${r(n)}`);
    case 8: return mk(0x3000 | (n << 8) | (m << 4), "cmp/eq", `${r(m)},${r(n)}`);
    case 9: return mk(0x2008 | (n << 8) | (m << 4), "tst", `${r(m)},${r(n)}`);
    case 10: case 11: {
      const room = 57 - k;
      if (room >= 3) {
        const d = 1 + ((h >>> 12) % Math.min(24, room - 1));
        const t = a + 4 + 2 * d;
        return mk((sel === 10 ? 0x8900 : 0x8B00) | d, sel === 10 ? "bt" : "bf", "loc_" + hex(t), { target: t });
      }
      return mk(0x6003 | (n << 8) | (m << 4), "mov", `${r(m)},${r(n)}`);
    }
    case 12: {
      const j = ((h >>> 12) % 25) - 12 || 3;
      let t = blockAddr + j * BLOCK;
      if (t < FLASH || t >= FLASH + FLASH_SIZE) t = blockAddr + BLOCK;
      const disp = (t - a - 4) >> 1;
      return mk(0xB000 | (disp & 0xfff), "bsr", nameOf(t), { target: t });
    }
    case 13: {
      const s = SYSCALLS[(h >>> 12) % SYSCALL_NAMES.length];
      return mk(0x400B | (n << 8), "jsr", "@" + r(n), { comment: "; → " + s.name, call: s.addr });
    }
    case 14: { const d = (h >>> 12) & 15; return mk(0x1000 | (n << 8) | (m << 4) | d, "mov.l", `${r(m)},@(${d * 4},${r(n)})`); }
    case 15: return mk(0x600C | (n << 8) | (m << 4), "extu.b", `${r(m)},${r(n)}`);
    default: return mk(0x4008 | (n << 8), "shll2", r(n));
  }
}

export const disasmProvider = {
  name: `Flash ${hex(FLASH)}–${hex(FLASH + FLASH_SIZE - 1)}`,
  count: BLOCKS * ROWS,
  row(i) {
    if (i < 0 || i >= this.count) return null;
    const b = Math.floor(i / ROWS), k = i % ROWS;
    const blockAddr = FLASH + b * BLOCK;
    if (blockAddr >= STR_AT && blockAddr < STR_END) {
      if (k === 0) return { kind: "label", addr: blockAddr, text: "str_" + hex(blockAddr), comment: "; text" };
      return insn(blockAddr + (k - 1) * 2);
    }
    if (k === 0) return { kind: "label", addr: blockAddr, text: nameOf(blockAddr), comment: `; ${1 + (h32(blockAddr) % 9)} callers` };
    return insn(blockAddr + (k - 1) * 2);
  },
  rowOf(addr) {
    if (addr < FLASH || addr >= FLASH + FLASH_SIZE) return -1;
    const off = (addr - FLASH) & ~1;
    return Math.floor(off / BLOCK) * ROWS + 1 + ((off % BLOCK) >> 1);
  },
  find(name) {
    const s = SYSCALLS.find((x) => x.name && x.name.toLowerCase() === name.toLowerCase());
    if (s) return s.addr;
    const m = /^(?:fn|loc|str)_([0-9a-f]{8})$/i.exec(name);
    return m ? parseInt(m[1], 16) >>> 0 : null;
  },
  symbol(addr) {
    if (addr < FLASH || addr >= FLASH + FLASH_SIZE) return "";
    const b = addr - ((addr - FLASH) % BLOCK);
    return nameOf(b) + (addr > b ? "+0x" + (addr - b).toString(16).toUpperCase() : "");
  },
};

// ---- memory ---------------------------------------------------------------------------
let epoch = 0; // bumped at every sample step, so some RAM bytes change
const REGIONS = [
  { name: "Flash", start: FLASH, size: FLASH_SIZE },
  { name: "RAM", start: 0x8C000000, size: 8 << 20 },
  { name: "IL RAM", start: 0xE5200000, size: 4 << 10 },
  { name: "X RAM", start: 0xE5007000, size: 8 << 10 },
  { name: "Y RAM", start: 0xE5017000, size: 8 << 10 },
];
function ramByte(a) {
  const h = h32(a >>> 2);
  // the stack (near r15): return addresses and saved registers, changing as it runs
  if (a >= 0x8C1FF000 && a < 0x8C200000) {
    const w = h32((a >>> 2) + (a >= state.regs.r15 - 32 && a < state.regs.r15 + 32 ? epoch : 0));
    const val = (w & 3) === 0 ? 0x80000000 | (w & 0x1ffffe) : w & 0x0000ffff;
    return (val >>> (24 - 8 * (a & 3))) & 0xff;
  }
  if ((h & 7) < 5) return 0;
  if ((h & 15) === 5) { // a pointer into RAM
    const v = (0x8C000000 | (h32(h) & 0x7ffffc)) >>> 0;
    return (v >>> (24 - 8 * (a & 3))) & 0xff;
  }
  return (h32(a) >>> 8) & 0xff;
}
export const memorySource = {
  regions: REGIONS,
  read(addr, len) {
    const out = new Uint8Array(len);
    for (let i = 0; i < len; i++) {
      const a = (addr + i) >>> 0;
      if (a >= FLASH && a < FLASH + FLASH_SIZE) {
        const w = insn(a & ~1).word ?? 0;
        out[i] = a & 1 ? w & 0xff : w >>> 8;
      } else if (a >= 0x8C000000 && a < 0x8C800000) out[i] = ramByte(a);
      else out[i] = (h32(a) & 3) === 0 ? h32(a + 1) & 0xff : 0;
    }
    return out;
  },
};

export const stringsProvider = {
  async search(q, { encoding }) {
    q = q.toLowerCase();
    return STRINGS.filter((s) => (encoding === "any" || s.encoding === encoding) && (!q || s.text.toLowerCase().includes(q)));
  },
};

// ---- a CPU to step ----------------------------------------------------------------------
const state = {
  pc: 0,
  regs: { r15: 0x8C1FFE60 },
  t: 0,
  frames: [], // [{ addr: return address, sp }], innermost last
  total: 0,
};

function initState() {
  const r = state.regs;
  for (let i = 0; i < 15; i++) r["r" + i] = h32(i * 31 + 7) & (i < 8 ? 0x0000ffff : 0x8C1fffff);
  r.r14 = 0x8C1FFE80; r.r15 = 0x8C1FFE60;
  Object.assign(r, {
    pr: 0, sr: 0x40000000, gbr: 0x8C0A2000, vbr: 0x80021000, mach: 0, macl: 0x00012C00,
    ssr: 0x40000001, spc: 0x8003A6C2, sgr: 0x8C1FFE20, dbr: 0x80021000,
    dsr: 0, a0: 0, a0g: 0, a1: 0, a1g: 0, m0: 0, m1: 0, x0: 0, x1: 0, y0: 0, y1: 0,
    rs: 0, re: 0, mod: 0,
  });
  for (let i = 0; i < 8; i++) r[`r${i}_bank`] = h32(i + 900) & 0xffff;
  // outer frames to return to; the walk below makes the trace
  const outer = ["GetKey", "Bdisp_PutDisp_DD", "MCS_SearchDirectoryItem"].map((n) => SYSCALLS.find((s) => s.name === n).addr);
  state.frames = outer.map((b, i) => ({ addr: b + 0x10 + 4 * i, sp: 0x8C1FFF40 - i * 0x30 }));
  state.pc = SYSCALLS.find((s) => s.name === "Bdisp_AllClr_VRAM").addr + 6;
  const trail = [];
  for (let i = 0; i < 900; i++) { trail.push(state.pc); step("into", true); }
  state.total = 1_284_392_117;
  return trail;
}

// step moves the sample CPU by one instruction ("into" / "over"); quiet = no UI updates.
function step(how, quiet = false) {
  const r = state.regs;
  const row = insn(state.pc);
  let next = state.pc + 2;
  if (row.mnem === "bsr") {
    if (how === "into" && !(quiet && state.frames.length >= 7)) { state.frames.push({ addr: state.pc + 4, sp: r.r15 }); r.pr = state.pc + 4; next = row.target; r.r15 -= 8; }
    else next = state.pc + 4;
  } else if (row.mnem === "rts") {
    const f = state.frames.pop();
    next = f ? f.addr : FLASH + (h32(state.pc) % BLOCKS) * BLOCK;
    r.r15 += 8;
  } else if (row.mnem === "bt" || row.mnem === "bf") {
    if ((row.mnem === "bt") === !!state.t) next = row.target;
  } else if (row.mnem === "cmp/eq" || row.mnem === "tst") {
    state.t = h32(state.pc + state.total) & 1;
    r.sr = (r.sr & ~1) | state.t;
  } else {
    const dest = /(?:^|,)(r\d+)$/.exec(row.ops || "");
    if (dest && dest[1] !== "r15") r[dest[1]] = h32(state.pc ^ (state.total + 1)) & 0x8C1fffff;
  }
  state.pc = next >>> 0;
  state.total++;
  if (!quiet) pushTrace(state.pc);
}

function registerGroups() {
  const r = state.regs;
  const g = (name, names) => ({ name, regs: names.map((n) => ({ name: n, value: r[n] ?? 0 })) });
  return [
    g("General", Array.from({ length: 16 }, (_, i) => "r" + i)),
    { name: "Control", regs: [{ name: "pc", value: state.pc }, ...["pr", "sr", "gbr", "vbr", "ssr", "spc", "sgr", "dbr"].map((n) => ({ name: n, value: r[n] }))] },
    g("Multiply", ["mach", "macl"]),
    g("Banked (other bank)", Array.from({ length: 8 }, (_, i) => `r${i}_bank`)),
    g("DSP", ["dsr", "a0", "a0g", "a1", "a1g", "m0", "m1", "x0", "x1", "y0", "y1", "rs", "re", "mod"]),
  ];
}

function frames() {
  const out = [{ addr: state.pc, name: disasmProvider.symbol(state.pc), sp: state.regs.r15 }];
  for (let i = state.frames.length - 1; i >= 0; i--) {
    const f = state.frames[i];
    out.push({ addr: f.addr, name: disasmProvider.symbol(f.addr), sp: f.sp });
  }
  return out;
}

function publish() {
  epoch++;
  setRegisters(registerGroups(), { sample: true });
  setPC(state.pc);
  setCallStack(frames(), { sample: true });
  refreshMemory();
  const b = dbg.breakpointAt(state.pc);
  if (b?.enabled) {
    dbg.setBreakpointHits({ [b.id]: b.hits + 1 });
    nav.log("warn", `Breakpoint at ${hex(state.pc)} (${disasmProvider.symbol(state.pc)})`);
  }
}

const backend = {
  async stepInto() { step("into"); publish(); },
  async stepOver() { step("over"); publish(); },
  async stepOut() {
    const depth = state.frames.length;
    for (let i = 0; i < 4000 && state.frames.length >= depth; i++) step("over");
    publish();
  },
  async runToCursor(addr) {
    for (let i = 0; i < 20000 && state.pc !== addr; i++) step("over");
    if (state.pc !== addr) { state.pc = addr; pushTrace(addr); }
    publish();
  },
  setBreakpoints() {},
};

export function installSample() {
  const trail = initState();
  setDisassembly(disasmProvider, { sample: true });
  setMemory(memorySource, { sample: true });
  setStrings(stringsProvider, { sample: true });
  setSyscalls(SYSCALLS, { sample: true });
  setTrace(trail, { total: state.total, sample: true });
  setRegisters(registerGroups(), { sample: true });
  setCallStack(frames(), { sample: true });
  setPC(state.pc, { reveal: false });
  dbg.setBackend(backend);
  if (!dbg.breakpoints().length) {
    dbg.addBreakpoint(SYSCALLS.find((s) => s.name === "Bfile_OpenFile_OS").addr + 4);
    dbg.addBreakpoint(SYSCALLS.find((s) => s.name === "GetKey").addr + 0x1A);
    const b = dbg.addBreakpoint(0x8C0A2010, "write");
    dbg.setBreakpointEnabled(b.id, false);
  }
}
