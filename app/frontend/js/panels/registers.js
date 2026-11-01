// The Registers window: grouped CPU registers, values that changed since the last stop
// highlighted, and the SR bits spelled out. A value's buttons open it in a memory view or the
// disassembly; double-click a value to change it (while stopped).
//
// Interface:
//   setRegisters(groups, { stale })   groups = [{ name: "General", regs: [{ name: "r0", value, bits: 32, title }] }]
//                          (a register named "sr" also gets its bits shown); stale: the
//                          machine runs on, these are the values at the last stop
//   setEditor(fn)          fn(name, value) → Promise, to change a register (null: read only)
//   onRegisters(fn)        fn(values: { name → value }) after every setRegisters (memory views
//                          that follow a register use it); returns an unsubscribe function
//   registerValues()       the current { name → value }
import { hex } from "../vlist.js";
import { pane, h } from "./pane.js";
import { nav } from "../nav.js";

export const meta = { kind: "registers", title: "Registers", multi: false };

let groups = [];
let stale = false;
let editor = null;
let values = {};
let changed = new Set();
const views = new Set();
const listeners = new Set();

export function setRegisters(g, { stale: st = false } = {}) {
  if (st) { // the same values, now from the last stop
    stale = true;
    for (const v of views) v.render();
    return;
  }
  const prev = values;
  const first = !groups.length;
  groups = g || [];
  stale = false;
  values = {};
  for (const grp of groups) for (const r of grp.regs) values[r.name] = r.value >>> 0;
  changed = new Set(first ? [] : Object.keys(values).filter((k) => prev[k] !== undefined && prev[k] !== values[k]));
  for (const v of views) v.render();
  for (const fn of listeners) fn(values);
}

export function setEditor(fn) { editor = fn; for (const v of views) v.render(); }

export function onRegisters(fn) { listeners.add(fn); return () => listeners.delete(fn); }
export function registerValues() { return values; }

// SR (SH-4A + DSP): name, bit, width
const SR_BITS = [["MD", 30, 1], ["RB", 29, 1], ["BL", 28, 1], ["FD", 15, 1], ["DSP", 12, 1], ["M", 9, 1], ["Q", 8, 1], ["IMASK", 4, 4], ["S", 1, 1], ["T", 0, 1]];

export function create() {
  const { el, tools, body } = pane("registers");
  const note = h("span.where.stale-note");
  tools.append(h("span.where", { html: '<span class="chg-key"></span>changed' , title: "Changed by the last step or stop" }), h("span.grow"), note);
  body.classList.add("scroll");

  function edit(cell, r) {
    if (!editor || stale) return;
    const bits = r.bits || 32;
    const inp = h("input.field.reg-edit", { value: hex(r.value, bits / 4), spellcheck: false, title: "Enter: set · Esc: cancel" });
    const rv = cell.querySelector(".rv");
    rv.replaceWith(inp);
    inp.focus();
    inp.select();
    const done = () => { if (inp.isConnected) inp.replaceWith(rv); };
    inp.addEventListener("keydown", async (e) => {
      e.stopPropagation();
      if (e.key === "Escape") { done(); return; }
      if (e.key !== "Enter") return;
      const t = inp.value.trim().replace(/_/g, "");
      const v = /^-?\d+$/.test(t) && t.startsWith("-") ? Number(t) >>> 0 : /^(0x)?[0-9a-f]{1,8}$/i.test(t) ? parseInt(t.replace(/^0x/i, ""), 16) >>> 0 : null;
      if (v === null) { inp.classList.add("bad"); return; }
      try { await editor(r.name, v); } catch (err) { inp.classList.add("bad"); inp.title = String(err); return; }
      done();
    });
    inp.addEventListener("blur", done);
  }

  const view = {
    render() {
      note.textContent = stale ? "running · as at the last stop" : "";
      el.classList.toggle("stale", stale);
      body.textContent = "";
      if (!groups.length) { body.append(h("div.empty", { text: "No registers yet: they show when the machine stops (pause it, or a breakpoint)." })); return; }
      for (const g of groups) {
        const sec = h("section.reg-group", {}, h("h4", { text: g.name }));
        const grid = h("div.reg-grid");
        for (const r of g.regs) {
          const bits = r.bits || 32;
          const cell = h("div.reg" + (changed.has(r.name) ? ".chg" : ""), { title: `${r.title ? r.title + "\n" : ""}${r.name} = ${r.value >>> 0} (0x${hex(r.value, bits / 4)})${editor && !stale ? "\nDouble-click to change it" : ""}` },
            h("span.rn", { text: r.name }),
            h("span.rv", { text: hex(r.value, bits / 4) }),
            h("span.go", {},
              h("button", { text: "mem", title: "Show this address in a memory view", onclick: () => nav.data(r.value >>> 0) }),
              h("button", { text: "code", title: "Show this address in the disassembly", onclick: () => nav.code(r.value >>> 0) })));
          cell.querySelector(".rv").addEventListener("dblclick", () => edit(cell, r));
          grid.append(cell);
        }
        sec.append(grid);
        if (g.regs.some((r) => r.name === "sr")) sec.append(srFlags(values.sr));
        body.append(sec);
      }
    },
  };
  function srFlags(sr) {
    const row = h("div.flags");
    for (const [name, bit, w] of SR_BITS) {
      const v = (sr >>> bit) & ((1 << w) - 1);
      const was = changed.has("sr");
      row.append(h("span.flag" + (v ? ".on" : "") + (was ? ".maybe" : ""), { title: `SR.${name} (bit ${bit}${w > 1 ? "–" + (bit + w - 1) : ""})`, text: w > 1 ? `${name} ${v}` : name }));
    }
    return row;
  }
  views.add(view);
  view.render();
  return { element: el, dispose: () => views.delete(view) };
}
