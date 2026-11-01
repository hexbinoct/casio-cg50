// The Call stack window: the frames from the stack and PR, innermost first. Click a frame
// to see it in the disassembly.
//
// Interface:
//   setCallStack(frames)   frames = [{ addr, name, sp }]   (addr: where that frame is now;
//                          frame 0 is the current function at the PC)
import { hex, esc } from "../vlist.js";
import { pane, h, sampleBadge } from "./pane.js";
import { nav } from "../nav.js";

export const meta = { kind: "callstack", title: "Call stack", multi: false };

let frames = [];
let isSample = false;
const views = new Set();

export function setCallStack(f, { sample = false } = {}) {
  frames = f || [];
  isSample = sample;
  for (const v of views) v.render();
}

export function create() {
  const { el, tools, body } = pane("callstack");
  const badge = sampleBadge();
  const count = h("span.where");
  tools.append(count, h("span.grow"), badge);
  body.classList.add("scroll");
  const table = h("div.table.cs-table");
  body.append(table);
  const view = {
    render() {
      badge.hidden = !isSample;
      count.textContent = frames.length ? `${frames.length} frames` : "";
      table.textContent = "";
      if (!frames.length) { table.append(h("div.empty", { text: "No call stack: the machine is running." })); return; }
      table.append(h("div.tr.th", { html: "<span>#</span><span>Function</span><span>Address</span><span>SP</span>" }));
      frames.forEach((f, i) => {
        table.append(h("div.tr.click" + (i === 0 ? ".cur" : ""), { onclick: () => nav.code(f.addr), title: "Show in the disassembly" },
          h("span.mono.dim", { text: String(i) }),
          h("span.mono.fn", { html: esc(f.name || "?") }),
          h("span.mono", { text: hex(f.addr) }),
          h("span.mono.dim", { text: f.sp != null ? hex(f.sp) : "" })));
      });
    },
  };
  views.add(view);
  view.render();
  return { element: el, dispose: () => views.delete(view) };
}
