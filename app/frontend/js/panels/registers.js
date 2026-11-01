// The Registers window: grouped CPU registers, values that changed since the last stop
// highlighted, and the SR bits spelled out. A value's arrows open it in a memory view or the
// disassembly.
//
// Interface:
//   setRegisters(groups)   groups = [{ name: "General", regs: [{ name: "r0", value, bits: 32 }] }]
//                          (a register named "sr" also gets its bits shown)
//   onRegisters(fn)        fn(values: { name → value }) after every setRegisters (memory views
//                          that follow a register use it); returns an unsubscribe function
//   registerValues()       the current { name → value }
import { hex } from "../vlist.js";
import { pane, h, sampleBadge } from "./pane.js";
import { nav } from "../nav.js";

export const meta = { kind: "registers", title: "Registers", multi: false };

let groups = [];
let isSample = false;
let values = {};
let changed = new Set();
const views = new Set();
const listeners = new Set();

export function setRegisters(g, { sample = false } = {}) {
  const prev = values;
  const first = !groups.length;
  groups = g || [];
  isSample = sample;
  values = {};
  for (const grp of groups) for (const r of grp.regs) values[r.name] = r.value >>> 0;
  changed = new Set(first ? [] : Object.keys(values).filter((k) => prev[k] !== undefined && prev[k] !== values[k]));
  for (const v of views) v.render();
  for (const fn of listeners) fn(values);
}

export function onRegisters(fn) { listeners.add(fn); return () => listeners.delete(fn); }
export function registerValues() { return values; }

// SR (SH-4A + DSP): name, bit, width
const SR_BITS = [["MD", 30, 1], ["RB", 29, 1], ["BL", 28, 1], ["FD", 15, 1], ["M", 9, 1], ["Q", 8, 1], ["IMASK", 4, 4], ["S", 1, 1], ["T", 0, 1]];

export function create() {
  const { el, tools, body } = pane("registers");
  const badge = sampleBadge();
  tools.append(h("span.where", { html: '<span class="chg-key"></span>changed at the last stop' }), h("span.grow"), badge);
  body.classList.add("scroll");

  const view = {
    render() {
      badge.hidden = !isSample;
      body.textContent = "";
      if (!groups.length) { body.append(h("div.empty", { text: "No registers: the machine is running or no dump is open." })); return; }
      for (const g of groups) {
        const sec = h("section.reg-group", {}, h("h4", { text: g.name }));
        const grid = h("div.reg-grid");
        for (const r of g.regs) {
          const bits = r.bits || 32;
          const cell = h("div.reg" + (changed.has(r.name) ? ".chg" : ""), { title: `${r.name} = ${r.value >>> 0} (0x${hex(r.value, bits / 4)})` },
            h("span.rn", { text: r.name }),
            h("span.rv", { text: hex(r.value, bits / 4) }),
            h("span.go", {},
              h("button", { text: "mem", title: "Show this address in a memory view", onclick: () => nav.data(r.value >>> 0) }),
              h("button", { text: "code", title: "Show this address in the disassembly", onclick: () => nav.code(r.value >>> 0) })));
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
