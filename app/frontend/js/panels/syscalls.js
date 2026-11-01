// The Syscalls window: the OS's syscall table, read from the OS image itself (number, name
// when known, the handler it leads to), searchable by number or name. Double-click (or
// Enter) opens a syscall's code in the disassembly; F9 or its dot breaks when an add-in calls
// it (a "syscall" breakpoint: the stop is at the OS's syscall routine, before the handler).
//
// Interface:
//   setSyscalls(list)        list = [{ num, name, addr }]   (name "" when unknown)
//   syscallNumber(text) → num | null   a syscall's number from its name (any case)
import { VList, esc, hex } from "../vlist.js";
import { pane, h, field } from "./pane.js";
import { nav } from "../nav.js";
import * as dbg from "../debugger.js";

export const meta = { kind: "syscalls", title: "Syscalls", multi: false };

let all = [];
const views = new Set();

export function setSyscalls(list) {
  all = (list || []).slice().sort((a, b) => a.num - b.num);
  for (const v of views) v.filter();
}

export function syscallNumber(text) {
  const t = String(text).trim().toLowerCase();
  const s = all.find((x) => x.name && x.name.toLowerCase() === t);
  return s ? s.num : null;
}
export function syscallName(num) { return all.find((x) => x.num === num)?.name || ""; }

function toggleBreak(s) {
  const b = dbg.syscallBreak(s.num);
  if (b) dbg.removeBreakpoint(b.id);
  else dbg.addBreakpoint(s.num, "syscall");
}

export function create(ctx) {
  const { el, tools, body } = pane("syscalls");
  let shown = [];
  const box = field("Number or name", () => {}, { width: 170 });
  box.value = ctx.params.q || "";
  box.classList.add("search");
  box.addEventListener("input", () => view.filter());
  const named = h("button.toggle" + (ctx.params.named ? ".on" : ""), { text: "Named", title: "Only the syscalls with a known name" });
  named.onclick = () => { named.classList.toggle("on"); view.filter(); };
  const count = h("span.where");
  tools.append(box, named, h("span.grow"), count);

  const host = h("div.list-host");
  const empty = h("div.empty", { text: "The syscall table shows once a flash dump is open and its OS is read." });
  body.append(h("div.list-head.sys", { html: '<span class="g"></span><span class="n">No.</span><span class="t">Name</span><span class="a">Handler</span>' }), host, empty);
  const list = new VList(host, {
    rowHeight: 20,
    render(row, i) {
      const s = shown[i];
      const b = dbg.syscallBreak(s.num);
      row.className = "vl-row sys" + (b ? (b.enabled ? " bp" : " bp-off") : "");
      row.innerHTML = `<span class="g" title="Break when an add-in calls it (F9)">${b ? '<span class="bp-dot"></span>' : ""}</span><span class="n">${hex(s.num, 4)}</span><span class="t">${s.name ? esc(s.name) : '<span class="dim">—</span>'}</span><span class="a">${hex(s.addr)}</span>`;
    },
  });
  list.onSelect = (i, ev) => {
    if (ev?.target?.closest?.(".g")) toggleBreak(shown[i]);
  };
  list.onActivate = (i) => nav.code(shown[i].addr);
  const unsub = dbg.onBreakpoints(() => list.invalidate());

  const view = {
    filter() {
      const q = box.value.trim().toLowerCase().replace(/^0x/, "");
      const onlyNamed = named.classList.contains("on");
      ctx.save({ q: box.value, named: onlyNamed });
      shown = all.filter((s) => (!onlyNamed || s.name) && (!q || s.name.toLowerCase().includes(q) || hex(s.num, 4).toLowerCase().startsWith(q)));
      count.textContent = all.length ? `${shown.length.toLocaleString()} of ${all.length.toLocaleString()}` : "";
      empty.hidden = all.length > 0;
      list.sel = -1;
      list.setCount(shown.length);
    },
  };
  views.add(view);
  view.filter();
  return {
    element: el,
    focus: () => box.focus(),
    dispose: () => { views.delete(view); unsub(); list.dispose(); },
    keydown: (e) => {
      if (e.target.tagName === "INPUT") return false;
      if (e.key === "F9" && list.sel >= 0) { toggleBreak(shown[list.sel]); return true; }
      return list.keydown(e);
    },
  };
}
