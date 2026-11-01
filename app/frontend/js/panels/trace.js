// The Trace window: the last N instructions executed, oldest first, the newest at the
// bottom; click one to see it in the disassembly.
//
// Interface:
//   setTrace(addrs, { total })   the PCs, oldest first (an array or a Uint32Array); total =
//                                instructions executed in all, for the step numbers
//   pushTrace(addr)              one more (the window keeps the last `capacity`)
import { VList, esc, hex } from "../vlist.js";
import { pane, h, sampleBadge, toolButton } from "./pane.js";
import { rowAt } from "./disasm.js";
import { nav } from "../nav.js";

export const meta = { kind: "trace", title: "Trace", multi: false };

let capacity = 4096;
let ring = [];
let total = 0;
let isSample = false;
const views = new Set();

export function setTrace(addrs, { total: t, sample = false } = {}) {
  ring = Array.from(addrs).slice(-capacity);
  total = t ?? ring.length;
  isSample = sample;
  for (const v of views) v.update(true);
}

export function pushTrace(addr) {
  ring.push(addr >>> 0);
  if (ring.length > capacity) ring.shift();
  total++;
  for (const v of views) v.update(false);
}

export function create() {
  const { el, tools, body } = pane("trace");
  let follow = true;
  const followBtn = h("button.toggle.on", { text: "Follow", title: "Keep the newest instruction in view" });
  followBtn.onclick = () => { follow = !follow; followBtn.classList.toggle("on", follow); if (follow) list.scrollTo(ring.length - 1, "nearest"); };
  const count = h("span.where");
  const badge = sampleBadge();
  tools.append(followBtn, toolButton("trash", "Clear", () => setTrace([], { total })), h("span.grow"), count, badge);
  const host = h("div.list-host");
  body.append(h("div.list-head.trc", { html: '<span class="n">Step</span><span class="a">Address</span><span class="i">Instruction</span>' }), host);
  const list = new VList(host, {
    rowHeight: 20,
    render(row, i) {
      const a = ring[i];
      const r = rowAt(a);
      const n = total - ring.length + i;
      row.className = "vl-row trc" + (i === ring.length - 1 ? " last" : "");
      row.innerHTML = `<span class="n">${n.toLocaleString()}</span><span class="a">${hex(a)}</span>` +
        `<span class="i">${r ? `<b>${esc(r.mnem)}</b> ${esc(r.ops || "")}` : ""}</span>`;
    },
  });
  list.onSelect = (i, ev) => { if (ev) nav.code(ring[i]); };
  list.onActivate = (i) => nav.code(ring[i]);
  const view = {
    update() {
      badge.hidden = !isSample;
      count.textContent = `last ${ring.length.toLocaleString()} of ${total.toLocaleString()}`;
      list.setCount(ring.length);
      if (follow) list.scrollTo(ring.length - 1, "nearest");
    },
  };
  views.add(view);
  view.update();
  return {
    element: el,
    focus: () => el.focus({ preventScroll: true }),
    dispose: () => { views.delete(view); list.dispose(); },
    keydown: (e) => list.keydown(e),
  };
}
