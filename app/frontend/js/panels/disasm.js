// The Disassembly window: a listing over a whole address space (the flash, its P2 mirror,
// the add-in, RAM, the on-chip memories), with the PC and breakpoint markers, branch targets
// to click and follow, back / forward through the places visited, and who refers to a line
// (X). The rows live in Go: they come in batches (paged.js), never one call per row. Several
// instances may be open.
//
// Interface (engine.js calls these):
//   setListings({ gen, spaces: [{ id, name, base, size, count, analysed }] }, source)
//       source = { rows(space, first, n) → Promise<[row]>, rowOf(space, addr) → Promise<i>,
//                  lookup(name) → Promise<addr>, xrefs(addr) → Promise<[{ from, kind, fn, text }]> }
//       row = { kind: "insn" | "label" | "data" | "fill", addr, bytes, mnem, ops, comment,
//               target (an address the operand refers to: a link), text (labels), unk }
//   invalidate()            drop the cached rows (after a stop: RAM code may have changed)
//   setPC(addr | null)      the current instruction (null while running)
//   goTo(addr)              show addr in the most recently used listing
//   cursor() → addr | null  the selected line (Run to cursor)
//   findName(name) → Promise<addr | null>
import { VList, esc, hex, parseAddr } from "../vlist.js";
import { Paged } from "../paged.js";
import { pane, h, toolButton, field, icon } from "./pane.js";
import * as dbg from "../debugger.js";

export const meta = { kind: "disasm", title: "Disassembly", multi: true };

let listings = { gen: 0, spaces: [] };
let source = null;
const caches = new Map(); // space id → Paged (shared by the views)
let pc = null;
const views = new Set();
let last = null; // the view used most recently

function cacheFor(space) {
  let c = caches.get(space.id);
  if (!c) {
    c = new Paged((first, n) => source.rows(space.id, first, n), { onLoad: () => { for (const v of views) if (v.space?.id === space.id) v.list.invalidate(); } });
    caches.set(space.id, c);
  }
  return c;
}

export function setListings(l, src) {
  const gen = l?.gen ?? 0;
  const changed = gen !== listings.gen || src !== source;
  listings = l || { gen: 0, spaces: [] };
  source = src;
  if (changed) caches.clear();
  for (const v of views) v.reload();
}

export function invalidate() {
  for (const c of caches.values()) c.clear();
  for (const v of views) v.list.invalidate();
}

export function setPC(addr, { reveal = true } = {}) {
  pc = addr == null ? null : addr >>> 0;
  for (const v of views) v.pcChanged(reveal && v === (last || v));
}

export async function findName(name) {
  const a = parseAddr(name);
  if (a != null) return a;
  if (!source) return null;
  try { return (await source.lookup(name.trim())) >>> 0; } catch (_) { return null; }
}

export function goTo(addr) { (last || [...views][0])?.goTo(addr); }
export function cursor() { return last?.cursor() ?? null; }
export function spaces() { return listings.spaces; }
dbg.setCursorSource(cursor);
dbg.onBreakpoints(() => { for (const v of views) v.list.invalidate(); });

// the space showing addr: the one in view if it has it, else the first that does
function spaceFor(addr, prefer) {
  const inside = (s) => s && addr >= s.base && addr - s.base < s.size;
  if (inside(prefer)) return prefer;
  return listings.spaces.find(inside) || null;
}

const REG = /\b(r1[0-5]|r[0-9]|r[0-7]_bank|pc|pr|sr|gbr|vbr|ssr|spc|sgr|dbr|mach|macl|dsr|a[01]g?|m[01]|x[01]|y[01]|rs|re|mod)\b/g;

function opsHTML(row) {
  let s = esc(row.ops || "");
  if (row.target != null && s && row.kind === "insn") {
    // the referenced address is the last operand (bsr fn, bt 0x…); not a register or @(…)
    const i = s.lastIndexOf(",");
    const head = i >= 0 && !/\)$/.test(s) ? s.slice(0, i + 1) : "";
    const tail = head ? s.slice(i + 1) : s;
    if (!/^(r\d|@)/.test(tail)) return colour(head) + `<span class="link" data-to="${row.target}">${tail}</span>`;
  }
  if (row.kind === "data") return `<span class="n">${s}</span>`;
  return colour(s);
}
function colour(s) {
  return s.replace(REG, '<span class="r">$1</span>').replace(/(#-?(?:0x)?[0-9A-Fa-f]+)/g, '<span class="n">$1</span>');
}
function commentHTML(row) {
  const c = esc(row.comment || "");
  if (row.target != null && row.kind !== "insn" && c) return `<span class="link" data-to="${row.target}">${c}</span>`;
  return c;
}

function renderRow(el, r) {
  if (!r) {
    el.className = "vl-row d d-wait";
    el.innerHTML = '<span class="g"></span><span class="a">········</span>';
    delete el.dataset.addr;
    return;
  }
  const code = r.kind === "insn";
  const b = code ? dbg.breakpointAt(r.addr) : null;
  const isPC = pc !== null && r.kind !== "label" && r.addr === pc;
  el.className = "vl-row d d-" + (r.kind === "fill" ? "data" : r.kind) + (r.unk ? " unk" : "") + (isPC ? " pc" : "") + (b ? (b.enabled ? " bp" : " bp-off") : "");
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
    `<span class="c">${commentHTML(r)}</span>`;
}

export function create(ctx) {
  const { el, tools, body } = pane("disasm");
  const back = [], fwd = [];
  const backBtn = toolButton("back", "Back (Backspace, Alt+←)", () => history(-1));
  const fwdBtn = toolButton("fwd", "Forward (Alt+→)", () => history(1));
  const goField = field("Go to address or name", async (v) => {
    const a = parseAddr(v, view.cursor() ?? 0) ?? (await findName(v));
    if (a == null || !(await view.goTo(a))) { goField.classList.add("bad"); return; }
    goField.classList.remove("bad");
    goField.value = "";
    el.focus();
  }, { width: 190 });
  goField.addEventListener("input", () => goField.classList.remove("bad"));
  const pcBtn = toolButton("target", "Go to the PC (Home)", () => { if (pc !== null) view.goTo(pc); });
  const xrefBtn = h("button.toggle", { text: "Xrefs", title: "Who refers to the selected line or function (X)", onclick: () => showXrefs() });
  const spaceSel = h("select.field", { title: "Address space" });
  spaceSel.addEventListener("change", () => {
    const s = listings.spaces[spaceSel.selectedIndex];
    if (s) { view.goTo(s.base, "top"); el.focus(); }
  });
  const where = h("span.where");
  tools.append(backBtn, fwdBtn, goField, pcBtn, xrefBtn, h("span.grow"), where, spaceSel);

  const head = h("div.list-head.d", { html: '<span class="g"></span><span class="a">Address</span><span class="b">Bytes</span><span class="m">Instruction</span><span class="o"></span><span class="c">Comment</span>' });
  const host = h("div.list-host");
  const empty = h("div.empty", { text: "Open a flash dump to see the code." });
  const xp = h("div.xref-panel");
  xp.hidden = true;
  body.append(head, host, empty, xp);

  let space = null, cache = null;
  let anchor = null; // { addr, how }: where the view is, kept across reloads (the analysis, an add-in)
  const list = new VList(host, { rowHeight: 20, render: (rowEl, i) => renderRow(rowEl, cache?.get(i)) });

  const rowAt = (i) => cache?.get(i);
  function addrOf(i) { return rowAt(i)?.addr ?? null; }
  function here() { return list.sel >= 0 ? addrOf(list.sel) : addrOf(list.top); }
  function syncButtons() { backBtn.disabled = !back.length; fwdBtn.disabled = !fwd.length; }

  function setSpace(s) {
    space = s;
    cache = s ? cacheFor(s) : null;
    spaceSel.selectedIndex = s ? listings.spaces.indexOf(s) : -1;
    where.textContent = s && !s.analysed && s.id.startsWith("flash") ? "analysing…" : "";
    list.sel = -1;
    list.setCount(s ? s.count : 0);
  }

  let seq = 0;
  async function show(addr, how = "center") {
    const s = spaceFor(addr >>> 0, space);
    if (!s || !source) return false;
    const my = ++seq;
    const i = await source.rowOf(s.id, addr >>> 0).catch(() => -1);
    if (my !== seq || i < 0) return false;
    if (s !== space) setSpace(s);
    list.select(i, how);
    anchor = { addr: addr >>> 0, how };
    if (how === "center" && cache?.get(i - 1)?.kind === "label") list.scrollTo(i - 1, "nearest");
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
      const r = rowAt(i);
      if (r?.kind === "insn") dbg.toggleBreakpoint(r.addr);
    }
  };
  list.onActivate = (i) => {
    const r = rowAt(i);
    if (r?.target != null) view.goTo(r.target);
  };
  let saveT = 0;
  list.onScroll = () => {
    const a0 = addrOf(list.top);
    if (a0 != null) anchor = { addr: a0, how: "top" };
    clearTimeout(saveT);
    saveT = setTimeout(() => { const a = addrOf(list.top); if (a != null) ctx.save({ addr: a }); }, 400);
  };

  // ---- who refers to it ----
  async function showXrefs() {
    let a = view.cursor();
    const r = list.sel >= 0 ? rowAt(list.sel) : null;
    if (r?.kind === "label") a = r.addr;
    if (a == null || !source) return;
    const refs = await source.xrefs(a).catch(() => []);
    xp.textContent = "";
    const close = h("button.tbtn", { title: "Close (Esc)", onclick: () => { xp.hidden = true; el.focus(); } });
    close.append(icon("close", 14));
    xp.append(h("div.xref-head", {}, h("span", { html: `<b>${refs.length.toLocaleString()}</b> reference${refs.length === 1 ? "" : "s"} to <span class="mono">${hex(a)}</span>` }), h("span.grow"), close));
    const box = h("div.xref-list.scroll");
    if (!refs.length) box.append(h("div.empty", { text: "Nothing in the analysed code refers to this address." }));
    for (const x of refs.slice(0, 2000)) {
      box.append(h("div.xref-row", { onclick: () => view.goTo(x.from), title: "Show it" },
        h("span.a.mono", { text: hex(x.from) }), h("span.k", { text: x.kind }),
        h("span.fn.mono", { text: x.fn || "" }), h("span.t.mono", { text: x.text || "" })));
    }
    xp.append(box);
    xp.hidden = false;
  }

  const view = {
    list,
    get space() { return space; },
    reload() {
      const keep = space ? anchor : null;
      const s = (space && listings.spaces.find((x) => x.id === space.id)) || null;
      empty.hidden = listings.spaces.length > 0;
      head.hidden = !listings.spaces.length;
      spaceSel.textContent = "";
      for (const x of listings.spaces) spaceSel.append(h("option", { text: `${x.name}  ${hex(x.base)}` }));
      spaceSel.hidden = !listings.spaces.length;
      if (!listings.spaces.length) { setSpace(null); return; }
      setSpace(s || null);
      // a new view: the PC when stopped, else where it was left
      const want = keep?.addr ?? pc ?? ctx.params.addr ?? listings.spaces[0].base;
      show(want, keep ? keep.how : pc === null && ctx.params.addr != null ? "top" : "center").then((ok) => { if (!ok && !space) setSpace(listings.spaces[0]); });
    },
    pcChanged(reveal) {
      if (reveal && pc !== null) {
        anchor = { addr: pc, how: "center" }; // a reload of the listings (an add-in arrived) keeps it
        const s = spaceFor(pc, space);
        if (s && s !== space) show(pc);
        else if (s && source) {
          const at = pc;
          source.rowOf(s.id, at).then((i) => {
            if (i >= 0 && pc === at && (i < list.top + 2 || i > list.top + list.visible - 3)) list.scrollTo(i, "center");
          }).catch(() => {});
        }
      }
      list.invalidate();
    },
    async goTo(addr, how = "center") {
      const h0 = here();
      const ok = await show(addr, how);
      if (ok) {
        if (h0 != null && h0 !== addr) { back.push(h0); if (back.length > 200) back.shift(); }
        fwd.length = 0;
        syncButtons();
      }
      return ok;
    },
    cursor() {
      if (list.sel < 0) return null;
      const r = rowAt(list.sel);
      if (!r) return null;
      return r.kind === "label" ? (rowAt(list.sel + 1)?.addr ?? r.addr) : r.addr;
    },
  };
  views.add(view);
  if (!last) last = view;
  view.reload();
  syncButtons();
  el.addEventListener("focusin", () => { last = view; });

  return {
    element: el,
    focus: () => el.focus({ preventScroll: true }),
    dispose: () => { views.delete(view); list.dispose(); if (last === view) last = [...views][0] || null; },
    keydown(e) {
      if (e.target.tagName === "INPUT" || e.target.tagName === "SELECT") return false;
      if (e.key === "Escape" && !xp.hidden) { xp.hidden = true; return true; }
      if (list.keydown(e)) return true;
      if (e.key === "F9" && !e.shiftKey) { const a = view.cursor(); if (a != null) dbg.toggleBreakpoint(a); return true; }
      if (e.key === "Backspace" || (e.altKey && e.key === "ArrowLeft")) { history(-1); return true; }
      if (e.altKey && e.key === "ArrowRight") { history(1); return true; }
      if (e.key === "Home" && !e.ctrlKey && !e.metaKey) { if (pc !== null) view.goTo(pc); return true; }
      if (e.key === "x" || e.key === "X") { showXrefs(); return true; }
      if (e.key === "g" || ((e.ctrlKey || e.metaKey) && e.key === "g")) { goField.focus(); goField.select(); return true; }
      return false;
    },
  };
}

