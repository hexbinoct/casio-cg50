// The Breakpoints window: every breakpoint, watchpoint and break-on-event (a syscall, an
// exception, an interrupt), on/off, hit counts, conditions and skip counts. The list itself
// lives in debugger.js (the disassembly's F9, the Syscalls window and the console change it
// too); Go keeps it per dump.
//
// Interface: none of its own; debugger.js drives it.
import { hex, parseAddr, esc } from "../vlist.js";
import { pane, h, field, icon, toolButton } from "./pane.js";
import * as dbg from "../debugger.js";
import { findName } from "./disasm.js";
import { syscallNumber } from "./syscalls.js";
import { nav } from "../nav.js";

export const meta = { kind: "breakpoints", title: "Breakpoints", multi: false };

const KINDS = {
  exec: ["Execute", "Address or function"],
  write: ["Write", "Address[:length]"],
  read: ["Read", "Address[:length]"],
  access: ["Read/write", "Address[:length]"],
  syscall: ["Syscall", "Number or name, * = all"],
  exception: ["Exception", "EXPEVT, * = all but TLB misses"],
  interrupt: ["Interrupt", "INTEVT, * = all"],
};

// parseSpec reads what was typed for a kind → { addr, any, len } or null.
export async function parseSpec(kind, text) {
  const t = text.trim();
  if (!t) return null;
  if (kind === "syscall" || kind === "exception" || kind === "interrupt") {
    if (t === "*" || t.toLowerCase() === "any" || t.toLowerCase() === "all") return { addr: 0, any: true };
    const n = parseAddr(t);
    if (n != null) return { addr: n, any: false };
    if (kind === "syscall") { const s = syscallNumber(t); return s == null ? null : { addr: s, any: false }; }
    return null;
  }
  if (kind === "exec") { const a = await findName(t); return a == null ? null : { addr: a }; }
  const m = /^(\S+?)(?:[:\s]+(\S+))?$/.exec(t);
  const a = m && parseAddr(m[1]);
  if (a == null) return null;
  const len = m[2] ? Number(/^0x/i.test(m[2]) ? parseInt(m[2], 16) : m[2]) : 1;
  return Number.isFinite(len) && len > 0 ? { addr: a, len: len >>> 0 } : null;
}

function target(b) {
  if (b.kind === "syscall") return b.any ? "all" : hex(b.addr, 4);
  if (b.kind === "exception" || b.kind === "interrupt") return b.any ? "all" : hex(b.addr, 3);
  if (b.kind !== "exec" && b.len > 1) return `${hex(b.addr)}:${b.len}`;
  return hex(b.addr);
}

export function create() {
  const { el, tools, body } = pane("breakpoints");
  const kindSel = h("select.field", { title: "What stops the machine" });
  for (const [k, [v]] of Object.entries(KINDS)) kindSel.append(h("option", { value: k, text: v }));
  const add = field(KINDS.exec[1], async (v) => {
    const spec = await parseSpec(kindSel.value, v);
    if (!spec) { add.classList.add("bad"); return; }
    add.classList.remove("bad");
    add.value = "";
    dbg.addBreakpoint(spec.addr, kindSel.value, spec);
  }, { width: 170 });
  kindSel.addEventListener("change", () => { add.placeholder = add.title = KINDS[kindSel.value][1]; });
  add.addEventListener("input", () => add.classList.remove("bad"));
  tools.append(kindSel, add, h("span.grow"), toolButton("trash", "Remove all", () => dbg.clearBreakpoints()));
  body.classList.add("scroll");

  // a cell that turns into a text field on double-click
  function editable(text, title, onSet, cls = "") {
    const span = h("span.mono.edit" + cls, { text: text || "—", title });
    span.addEventListener("dblclick", () => {
      const inp = h("input.field.cell-edit", { value: text || "", spellcheck: false });
      span.replaceWith(inp);
      inp.focus();
      const end = (ok) => { if (ok) onSet(inp.value.trim()); if (inp.isConnected) inp.replaceWith(span); };
      inp.addEventListener("keydown", (e) => { e.stopPropagation(); if (e.key === "Enter") end(true); if (e.key === "Escape") end(false); });
      inp.addEventListener("blur", () => end(true));
    });
    return span;
  }

  const table = h("div.table.bp-table");
  body.append(table);
  const unsub = dbg.onBreakpoints((list) => {
    table.textContent = "";
    if (!list.length) {
      table.append(h("div.empty", { html: "No breakpoints. Press <kbd>F9</kbd> on a line of the disassembly, click its left edge, break on a syscall from the Syscalls window, or add one above." }));
      return;
    }
    table.append(h("div.tr.th", { html: "<span></span><span>Kind</span><span>At</span><span>Where</span><span>Hits</span><span>Condition</span><span>Skip</span><span></span>" }));
    for (const b of list) {
      const on = h("input", { type: "checkbox", title: "On / off" });
      on.checked = b.enabled;
      on.addEventListener("change", () => dbg.setBreakpointEnabled(b.id, on.checked));
      const del = h("button.tbtn", { title: "Remove", onclick: () => dbg.removeBreakpoint(b.id) });
      del.append(icon("trash", 14));
      const exec = b.kind === "exec";
      const addrCell = h("span.mono" + (b.kind === "syscall" && !b.any ? ".link" : exec || b.kind === "read" || b.kind === "write" || b.kind === "access" ? ".link" : ""), { text: target(b), title: b.note || "" });
      if (exec) addrCell.onclick = () => nav.code(b.addr);
      else if (b.kind === "read" || b.kind === "write" || b.kind === "access") addrCell.onclick = () => nav.data(b.addr);
      const cond = exec ? editable(b.condition, b.error || "Double-click: a condition over the registers, e.g. r4 == 0x10 && pr > 0x80000000", (v) => dbg.updateBreakpoint(b.id, { condition: v }), b.error ? ".bad" : "")
        : h("span.dim", { text: "—" });
      const skip = exec ? editable(b.ignore ? String(b.ignore) : "", "Double-click: how many hits pass before it stops", (v) => dbg.updateBreakpoint(b.id, { ignore: Math.max(0, parseInt(v, 10) || 0) }))
        : h("span.dim", { text: "" });
      const row = h("div.tr" + (b.enabled ? "" : ".off"), {},
        h("span", {}, on),
        h("span", {}, h("span.kind.k-" + b.kind, { text: KINDS[b.kind]?.[0] || b.kind })),
        addrCell,
        h("span.mono.dim", { html: esc(b.where || ""), title: b.note || b.where || "" }),
        h("span.mono", { text: String(b.hits || 0) }),
        cond, skip,
        h("span", {}, del));
      table.append(row);
    }
  });
  return { element: el, dispose: unsub };
}
