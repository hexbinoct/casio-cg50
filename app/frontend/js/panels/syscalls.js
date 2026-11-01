// The Syscalls window: the OS's syscall table, searchable by number or name; a syscall opens
// at its code in the disassembly.
//
// Interface:
//   setSyscalls(list)   list = [{ num, name, addr }]   (name "" when unknown)
import { VList, esc, hex } from "../vlist.js";
import { pane, h, field, sampleBadge } from "./pane.js";
import { nav } from "../nav.js";

export const meta = { kind: "syscalls", title: "Syscalls", multi: false };

let all = [];
let isSample = false;
const views = new Set();

export function setSyscalls(list, { sample = false } = {}) {
  all = (list || []).slice().sort((a, b) => a.num - b.num);
  isSample = sample;
  for (const v of views) v.filter();
}

export function create(ctx) {
  const { el, tools, body } = pane("syscalls");
  let shown = [];
  const box = field("Number or name", () => {}, { width: 170 });
  box.value = ctx.params.q || "";
  box.classList.add("search");
  box.addEventListener("input", () => view.filter());
  const count = h("span.where");
  const badge = sampleBadge();
  tools.append(box, h("span.grow"), count, badge);

  const host = h("div.list-host");
  body.append(h("div.list-head.sys", { html: '<span class="n">No.</span><span class="t">Name</span><span class="a">Code at</span>' }), host);
  const list = new VList(host, {
    rowHeight: 20,
    render(row, i) {
      const s = shown[i];
      row.className = "vl-row sys";
      row.innerHTML = `<span class="n">${hex(s.num, 4)}</span><span class="t">${s.name ? esc(s.name) : '<span class="dim">—</span>'}</span><span class="a">${hex(s.addr)}</span>`;
    },
  });
  list.onSelect = (i, ev) => { if (ev) nav.code(shown[i].addr); };
  list.onActivate = (i) => nav.code(shown[i].addr);

  const view = {
    filter() {
      badge.hidden = !isSample;
      const q = box.value.trim().toLowerCase().replace(/^0x/, "");
      ctx.save({ q: box.value });
      shown = !q ? all : all.filter((s) => s.name.toLowerCase().includes(q) || hex(s.num, 4).toLowerCase().startsWith(q));
      count.textContent = `${shown.length.toLocaleString()} of ${all.length.toLocaleString()}`;
      list.sel = -1;
      list.setCount(shown.length);
    },
  };
  views.add(view);
  view.filter();
  return {
    element: el,
    focus: () => box.focus(),
    dispose: () => { views.delete(view); list.dispose(); },
    keydown: (e) => (e.target.tagName === "INPUT" ? false : list.keydown(e)),
  };
}
