// The Call stack window: the frames from the stack and PR, innermost first, as the core
// rebuilds them at a stop (a heuristic: docs/DEBUG_API.md). Click a frame to see it in the
// disassembly. A frame whose call could not be checked is marked "?".
//
// Interface:
//   setCallStack(frames, { stale })   frames = [{ addr, name, sp, source, confirmed }]
//                          (addr: where that frame resumes; frame 0 is the PC)
import { hex, esc } from "../vlist.js";
import { pane, h } from "./pane.js";
import { nav } from "../nav.js";

export const meta = { kind: "callstack", title: "Call stack", multi: false };

let frames = [];
let stale = false;
const views = new Set();

export function setCallStack(f, { stale: st = false } = {}) {
  if (!st) frames = f || [];
  stale = st;
  for (const v of views) v.render();
}

const SRC = { pc: "PC", pr: "PR", stack: "stack" };

export function create() {
  const { el, tools, body } = pane("callstack");
  const count = h("span.where");
  tools.append(count, h("span.grow"), h("span.where.stale-note"));
  body.classList.add("scroll");
  const table = h("div.table.cs-table");
  body.append(table);
  const view = {
    render() {
      el.classList.toggle("stale", stale);
      tools.querySelector(".stale-note").textContent = stale ? "running: as at the last stop" : "";
      count.textContent = frames.length ? `${frames.length} frame${frames.length > 1 ? "s" : ""}` : "";
      table.textContent = "";
      if (!frames.length) { table.append(h("div.empty", { text: "No call stack yet: it shows when the machine stops." })); return; }
      table.append(h("div.tr.th", { html: "<span>#</span><span>Function</span><span>Address</span><span>From</span><span>SP</span>" }));
      frames.forEach((f, i) => {
        const sure = i === 0 || f.confirmed;
        table.append(h("div.tr.click" + (i === 0 ? ".cur" : ""), {
          onclick: () => nav.code(f.addr),
          title: i === 0 ? "The PC" : (sure ? "The call before it was checked" : "Not checked: a jsr/bsrf call (the target is unknown), or a stale word on the stack") + "\nClick: show in the disassembly",
        },
          h("span.mono.dim", { text: String(i) }),
          h("span.mono.fn", { html: esc(f.name || "?") + (sure ? "" : ' <span class="dim">?</span>') }),
          h("span.mono", { text: hex(f.addr) }),
          h("span.mono.dim", { text: SRC[f.source] || "" }),
          h("span.mono.dim", { text: f.source === "stack" && f.sp ? hex(f.sp) : "" })));
      });
    },
  };
  views.add(view);
  view.render();
  return { element: el, dispose: () => views.delete(view) };
}
