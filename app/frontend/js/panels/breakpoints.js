// The Breakpoints window: every breakpoint and watchpoint, on/off, hit counts, conditions.
// The list itself lives in debugger.js (the disassembly's F9 and gutter clicks change it too).
//
// Interface: none of its own; debugger.js (addBreakpoint, setBreakpointHits, …) drives it.
import { hex, parseAddr, esc } from "../vlist.js";
import { pane, h, field, icon, toolButton } from "./pane.js";
import * as dbg from "../debugger.js";
import { symbolAt } from "./disasm.js";
import { nav } from "../nav.js";

export const meta = { kind: "breakpoints", title: "Breakpoints", multi: false };

const KINDS = { exec: "Execute", read: "Read", write: "Write", access: "Read/write" };

export function create() {
  const { el, tools, body } = pane("breakpoints");
  const kindSel = h("select.field", { title: "What stops the machine" });
  for (const [k, v] of Object.entries(KINDS)) kindSel.append(h("option", { value: k, text: v }));
  const add = field("Add at address", (v) => {
    const a = parseAddr(v);
    if (a == null) { add.classList.add("bad"); return; }
    add.classList.remove("bad");
    add.value = "";
    dbg.addBreakpoint(a, kindSel.value);
  }, { width: 130 });
  add.addEventListener("input", () => add.classList.remove("bad"));
  tools.append(add, kindSel, h("span.grow"), toolButton("trash", "Remove all", () => dbg.clearBreakpoints()));
  body.classList.add("scroll");

  const table = h("div.table.bp-table");
  body.append(table);
  const unsub = dbg.onBreakpoints((list) => {
    table.textContent = "";
    if (!list.length) {
      table.append(h("div.empty", { html: "No breakpoints. Press <kbd>F9</kbd> on a line of the disassembly, click its left edge, or add one above." }));
      return;
    }
    table.append(h("div.tr.th", { html: "<span></span><span>Kind</span><span>Address</span><span>Where</span><span>Hits</span><span>Condition</span><span></span>" }));
    for (const b of list) {
      const on = h("input", { type: "checkbox", title: "On / off" });
      on.checked = b.enabled;
      on.addEventListener("change", () => dbg.setBreakpointEnabled(b.id, on.checked));
      const del = h("button.tbtn", { title: "Remove", onclick: () => dbg.removeBreakpoint(b.id) });
      del.append(icon("trash", 14));
      const row = h("div.tr" + (b.enabled ? "" : ".off"), {},
        h("span", {}, on),
        h("span", {}, h("span.kind.k-" + b.kind, { text: KINDS[b.kind] || b.kind })),
        h("span.mono.link", { text: hex(b.addr), title: "Show in the " + (b.kind === "exec" ? "disassembly" : "memory view"), onclick: () => (b.kind === "exec" ? nav.code(b.addr) : nav.data(b.addr)) }),
        h("span.mono.dim", { html: esc(symbolAt(b.addr) || "") }),
        h("span.mono", { text: String(b.hits) }),
        h("span.mono.dim", { text: b.condition || "—" }),
        h("span", {}, del));
      table.append(row);
    }
  });
  return { element: el, dispose: unsub };
}
