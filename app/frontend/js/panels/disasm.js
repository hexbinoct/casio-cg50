// The Disassembly window: a listing over a whole address space (virtual: only the rows in
// view are built), with the PC and breakpoint markers, branch targets to click and follow,
// and back / forward through the places visited. Several instances may be open.
//
// Interface (the debug engine calls these; today sample.js does):
//   setDisassembly(provider)   what to list; provider = {
//       name,                    shown in the toolbar, e.g. "Flash 80000000–81FFFFFF"
//       count,                   number of rows
//       row(i) → { kind: "insn" | "label" | "data", addr, bytes, mnem, ops, comment,
//                  target,       an address the operand refers to (branch, call): a link
//                  text }        label rows: the name
//       rowOf(addr) → i,         the row showing addr (its instruction, not its label)
//       find?(name) → addr,      names typed in the go-to box
//       symbol?(addr) → "fn+0x1C" } the nearest name, for the other windows
//   setPC(addr | null)         the current instruction (null while running)
//   goTo(addr)                 show addr in the most recently used listing
//   cursor() → addr | null     the selected line (Run to cursor)
import { VList, esc, hex, parseAddr } from "../vlist.js";
import { pane, h, toolButton, field, sampleBadge } from "./pane.js";
import * as dbg from "../debugger.js";

export const meta = { kind: "disasm", title: "Disassembly", multi: true };

const EMPTY = { name: "", count: 0, row: () => null, rowOf: () => -1 };
let provider = EMPTY;
let isSample = false;
let pc = null;
const views = new Set();
let last = null; // the view used most recently

export function setDisassembly(p, { sample = false } = {}) {
  provider = p || EMPTY;
  isSample = sample;
  for (const v of views) v.reload();
}

export function setPC(addr, { reveal = true } = {}) {
  pc = addr == null ? null : addr >>> 0;
  for (const v of views) v.pcChanged(reveal);
}

export function rowAt(addr) { const i = provider.rowOf(addr >>> 0); return i >= 0 ? provider.row(i) : null; }
export function findName(name) { return provider.find?.(name) ?? null; }
export function symbolAt(addr) { return provider.symbol?.(addr >>> 0) ?? ""; }

export function goTo(addr) { (last || [...views][0])?.goTo(addr); }
export function cursor() { return last?.cursor() ?? null; }
dbg.setCursorSource(cursor);
dbg.onBreakpoints(() => { for (const v of views) v.list.invalidate(); });

const REG = /\b(r1[0-5]|r[0-9]|r[0-7]_bank|pc|pr|sr|gbr|vbr|ssr|spc|sgr|dbr|mach|macl|dsr|a[01]g?|m[01]|x[01]|y[01]|rs|re|mod)\b/g;

function opsHTML(row) {
  let s = esc(row.ops || "");
  if (row.target != null && s) {
    // the referenced address is the last operand (bsr fn, bt loc, mov.l @(d,pc),rn → the literal)
    const i = s.lastIndexOf(",");
    const head = i >= 0 && !/\)$/.test(s) ? s.slice(0, i + 1) : "";
    const tail = head ? s.slice(i + 1) : s;
    if (!/^(r\d|@)/.test(tail)) return colour(head) + `<span class="link" data-to="${row.target}">${tail}</span>`;
  }
  return colour(s);
}
function colour(s) {
  return s.replace(REG, '<span class="r">$1</span>').replace(/(#-?(?:0x)?[0-9A-Fa-f]+)/g, '<span class="n">$1</span>');
}

function renderRow(el, i) {
  const r = provider.row(i);
  if (!r) { el.className = "vl-row d"; el.innerHTML = ""; return; }
  const b = r.kind === "insn" ? dbg.breakpointAt(r.addr) : null;
  const isPC = pc !== null && r.kind === "insn" && r.addr === pc;
  el.className = "vl-row d d-" + r.kind + (isPC ? " pc" : "") + (b ? (b.enabled ? " bp" : " bp-off") : "");
  el.dataset.addr = r.addr;
  const mark = isPC ? '<span class="pc-arrow"></span>' : "";
  if (r.kind === "label") {
    el.innerHTML = `<span class="g"></span><span class="a">${hex(r.addr)}</span><span class="lbl">${esc(r.text)}:</span>` +
      (r.comment ? `<span class="c">${esc(r.comment)}</span>` : "");
    return;
  }
  el.innerHTML = `<span class="g">${b ? '<span class="bp-dot"></span>' : ""}${mark}</span>` +
    `<span class="a">${hex(r.addr)}</span><span class="b">${esc(r.bytes || "")}</span>` +
    `<span class="m">${esc(r.mnem || "")}</span><span class="o">${opsHTML(r)}</span>` +
    `<span class="c">${r.comment ? esc(r.comment) : ""}</span>`;
}

export function create(ctx) {
  const { el, tools, body } = pane("disasm");
  const back = [], fwd = [];
  const backBtn = toolButton("back", "Back (Backspace, Alt+←)", () => history(-1));
  const fwdBtn = toolButton("fwd", "Forward (Alt+→)", () => history(1));
  const goField = field("Go to address or name", (v) => {
    const a = parseAddr(v, cursor() ?? 0) ?? provider.find?.(v.trim());
    if (a == null) { goField.classList.add("bad"); return; }
    goField.classList.remove("bad");
    goField.value = "";
    view.goTo(a);
    el.focus();
  }, { width: 190 });
  goField.addEventListener("input", () => goField.classList.remove("bad"));
  const pcBtn = toolButton("target", "Go to the PC (Home)", () => { if (pc !== null) view.goTo(pc); });
  const where = h("span.where");
  const badge = sampleBadge();
  tools.append(backBtn, fwdBtn, goField, pcBtn, h("span.grow"), where, badge);

  const head = h("div.list-head.d", { html: '<span class="g"></span><span class="a">Address</span><span class="b">Bytes</span><span class="m">Instruction</span><span class="o"></span><span class="c">Comment</span>' });
  const host = h("div.list-host");
  body.append(head, host);
  const list = new VList(host, { rowHeight: 20, render: renderRow });

  function addrOf(i) { return provider.row(i)?.addr ?? null; }
  function here() { return list.sel >= 0 ? addrOf(list.sel) : addrOf(list.top); }
  function syncButtons() { backBtn.disabled = !back.length; fwdBtn.disabled = !fwd.length; }

  function show(addr, how = "center") {
    const i = provider.rowOf(addr >>> 0);
    if (i < 0) return false;
    const prev = provider.row(i - 1);
    list.select(i, how);
    if (prev?.kind === "label" && how === "center" && list.sel === i) list.scrollTo(i - 1, "nearest");
    return true;
  }

  function history(dir) {
    const from = dir < 0 ? back : fwd, to = dir < 0 ? fwd : back;
    if (!from.length) return;
    const h0 = here();
    if (h0 != null) to.push(h0);
    show(from.pop());
    syncButtons();
  }

  list.onSelect = (i, ev) => {
    last = view;
    const link = ev?.target?.closest?.(".link");
    if (link) { view.goTo(Number(link.dataset.to)); return; }
    if (ev?.target?.closest?.(".g")) {
      const r = provider.row(i);
      if (r?.kind === "insn") dbg.toggleBreakpoint(r.addr);
    }
  };
  list.onActivate = (i) => {
    const r = provider.row(i);
    if (r?.target != null) view.goTo(r.target);
  };
  let saveT = 0;
  list.onScroll = () => {
    clearTimeout(saveT);
    saveT = setTimeout(() => { const a = addrOf(list.top); if (a != null) ctx.save({ addr: a }); }, 400);
  };

  const view = {
    list,
    reload() {
      where.textContent = provider.name || "";
      badge.hidden = !isSample;
      list.setCount(provider.count);
      head.hidden = !provider.count;
    },
    pcChanged(reveal) {
      if (reveal && pc !== null && view === (last || view)) {
        const i = provider.rowOf(pc);
        if (i >= 0 && (i < list.top + 2 || i > list.top + list.visible - 3)) list.scrollTo(i, "center");
      }
      list.invalidate();
    },
    goTo(addr) {
      const h0 = here();
      if (show(addr)) {
        if (h0 != null && h0 !== addr) { back.push(h0); if (back.length > 200) back.shift(); }
        fwd.length = 0;
        syncButtons();
      }
    },
    cursor() { return list.sel >= 0 ? (provider.row(list.sel)?.kind === "insn" ? addrOf(list.sel) : addrOf(list.sel + 1)) : null; },
  };
  views.add(view);
  if (!last) last = view;
  view.reload();
  syncButtons();
  if (ctx.params.addr != null) list.scrollTo(Math.max(0, provider.rowOf(ctx.params.addr) - 1), "top");
  else if (pc !== null) show(pc);
  el.addEventListener("focusin", () => { last = view; });

  return {
    element: el,
    focus: () => el.focus({ preventScroll: true }),
    dispose: () => { views.delete(view); list.dispose(); if (last === view) last = [...views][0] || null; },
    keydown(e) {
      if (e.target.tagName === "INPUT") return false;
      if (list.keydown(e)) return true;
      if (e.key === "F9" && !e.shiftKey) { const a = view.cursor(); if (a != null) dbg.toggleBreakpoint(a); return true; }
      if (e.key === "Backspace" || (e.altKey && e.key === "ArrowLeft")) { history(-1); return true; }
      if (e.altKey && e.key === "ArrowRight") { history(1); return true; }
      if (e.key === "Home" && !e.ctrlKey && !e.metaKey) { if (pc !== null) view.goTo(pc); return true; }
      if (e.key === "g" || ((e.ctrlKey || e.metaKey) && e.key === "g")) { goField.focus(); goField.select(); return true; }
      return false;
    },
  };
}
