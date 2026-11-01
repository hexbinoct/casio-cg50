// The Trace window: the last instructions executed, oldest first, the newest at the bottom;
// click one to see it in the disassembly. Recording is the core's trace ring: on while this
// window is open and Record is on (it costs about 40 % of the emulator's top speed, nothing
// at 1×). The list is fetched at every stop.
//
// Interface:
//   setTrace(entries, { total })   entries = [{ addr, text }], oldest first; total =
//                                  instructions recorded in all, for the step numbers
//   onRecord(fn)                   fn(on) when recording should start or stop
//   recording() → bool
import { VList, esc, hex } from "../vlist.js";
import { pane, h, toolButton } from "./pane.js";
import { nav } from "../nav.js";

export const meta = { kind: "trace", title: "Trace", multi: false };

let ring = [];
let total = 0;
let record = true;
let open = 0;
const views = new Set();
const recListeners = new Set();

export function setTrace(entries, { total: t } = {}) {
  ring = Array.from(entries || []);
  total = t ?? ring.length;
  for (const v of views) v.update();
}

export function onRecord(fn) { recListeners.add(fn); fn(recording()); return () => recListeners.delete(fn); }
export function recording() { return record && open > 0; }
function recChanged() { for (const fn of recListeners) fn(recording()); }

export function create(ctx) {
  const { el, tools, body } = pane("trace");
  if (ctx.params.record === false) record = false;
  let follow = true;
  const recBtn = h("button.toggle" + (record ? ".on" : ""), { text: "Record", title: "Record the instructions executed (costs some top speed)" });
  recBtn.onclick = () => { record = !record; recBtn.classList.toggle("on", record); ctx.save({ record }); recChanged(); view.update(); };
  const followBtn = h("button.toggle.on", { text: "Follow", title: "Keep the newest instruction in view" });
  followBtn.onclick = () => { follow = !follow; followBtn.classList.toggle("on", follow); if (follow) list.scrollTo(ring.length - 1, "nearest"); };
  const count = h("span.where");
  tools.append(recBtn, followBtn, toolButton("trash", "Clear the view", () => setTrace([], { total })), h("span.grow"), count);
  const host = h("div.list-host");
  const empty = h("div.empty");
  body.append(h("div.list-head.trc", { html: '<span class="n">Step</span><span class="a">Address</span><span class="i">Instruction</span>' }), host, empty);
  const list = new VList(host, {
    rowHeight: 20,
    render(row, i) {
      const r = ring[i];
      const n = total - ring.length + i;
      const [m, ...ops] = (r.text || "").split(" ");
      row.className = "vl-row trc" + (i === ring.length - 1 ? " last" : "");
      row.innerHTML = `<span class="n">${n.toLocaleString()}</span><span class="a">${hex(r.addr)}</span>` +
        `<span class="i"><b>${esc(m)}</b> ${esc(ops.join(" "))}</span>`;
    },
  });
  list.onSelect = (i, ev) => { if (ev) nav.code(ring[i].addr); };
  list.onActivate = (i) => nav.code(ring[i].addr);
  const view = {
    update() {
      count.textContent = ring.length ? `last ${ring.length.toLocaleString()} of ${total.toLocaleString()}` : "";
      empty.hidden = ring.length > 0;
      empty.textContent = record ? "Recording: the instructions show at the next stop." : "Not recording: turn Record on.";
      list.setCount(ring.length);
      if (follow) list.scrollTo(ring.length - 1, "nearest");
    },
  };
  views.add(view);
  open++;
  recChanged();
  view.update();
  return {
    element: el,
    focus: () => el.focus({ preventScroll: true }),
    dispose: () => { views.delete(view); list.dispose(); open--; recChanged(); },
    keydown: (e) => list.keydown(e),
  };
}
