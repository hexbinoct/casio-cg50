// The Memory window: hex and text over one memory region at a time (virtual: only the rows
// in view are built), go to an address, follow a register, bytes that changed since the
// last refresh highlighted, and the selected bytes read as numbers. Several instances.
//
// Interface:
//   setMemory(source)   source = {
//       regions: [{ name, start, size }],       what can be shown
//       read(addr, len) → Uint8Array | Promise<Uint8Array> | null   (null: not readable) }
//   refresh()           read again (after a stop); changed bytes are marked
//   goTo(addr)          the most recently used memory view shows addr
// A view following a register moves when registers.setRegisters reports a new value.
import { VList, hex, parseAddr } from "../vlist.js";
import { pane, h, field, sampleBadge } from "./pane.js";
import { onRegisters, registerValues } from "./registers.js";

export const meta = { kind: "memory", title: "Memory", multi: true };

const PAGE = 4096;
const EMPTY = { regions: [], read: () => null };
let source = EMPTY;
let isSample = false;
let cache = new Map(); // page address → Uint8Array | null | "wait"
let prev = new Map();  // the cache before the last refresh (to mark changes)
const views = new Set();
let last = null;

export function setMemory(src, { sample = false } = {}) {
  source = src || EMPTY;
  isSample = sample;
  cache = new Map();
  prev = new Map();
  for (const v of views) v.reload();
}

export function refresh() {
  prev = cache;
  cache = new Map();
  for (const v of views) v.list.invalidate();
}

export function goTo(addr) { (last || [...views][0])?.goTo(addr); }

function page(p) {
  let b = cache.get(p);
  if (b !== undefined) return b;
  let r = null;
  try { r = source.read(p, PAGE); } catch (_) { r = null; }
  if (r && typeof r.then === "function") {
    cache.set(p, "wait");
    r.then((bytes) => { cache.set(p, bytes || null); for (const v of views) v.list.invalidate(); },
      () => { cache.set(p, null); for (const v of views) v.list.invalidate(); });
    return "wait";
  }
  cache.set(p, r || null);
  return r || null;
}

function byteAt(addr) {
  const b = page(addr - (addr % PAGE));
  return b && b !== "wait" ? b[addr % PAGE] : null;
}

function prevByte(addr) {
  const b = prev.get(addr - (addr % PAGE));
  return b && b !== "wait" ? b[addr % PAGE] : undefined;
}

const FOLLOW = ["", "pc", "pr", "r15", "r4", "r5", "r6", "r7", "r14", "gbr"];

export function create(ctx) {
  const { el, tools, body } = pane("memory");
  let region = null;
  let sel = null; // selected byte address
  let follow = FOLLOW.includes(ctx.params.follow) ? ctx.params.follow : "";

  const regionSel = h("select.field", { title: "Memory region" });
  regionSel.addEventListener("change", () => {
    const r = source.regions[regionSel.selectedIndex];
    if (r) { view.goTo(r.start); el.focus(); }
  });
  const goField = field("Go to address", (v) => {
    const a = parseAddr(v, sel ?? region?.start ?? 0);
    if (a == null || !view.goTo(a)) { goField.classList.add("bad"); return; }
    goField.classList.remove("bad");
    goField.value = "";
    el.focus();
  }, { width: 130 });
  goField.addEventListener("input", () => goField.classList.remove("bad"));
  const followSel = h("select.field", { title: "Follow a register: the view moves to its value at every stop" });
  for (const f of FOLLOW) followSel.append(h("option", { value: f, text: f ? "Follow " + f : "Fixed address" }));
  followSel.value = follow;
  followSel.addEventListener("change", () => {
    follow = followSel.value;
    ctx.save({ follow });
    if (follow) view.followNow();
  });
  const badge = sampleBadge();
  tools.append(regionSel, goField, followSel, h("span.grow"), badge);

  const head = h("div.list-head.mem", { html: '<span class="a">Address</span><span class="hx">' +
    Array.from({ length: 16 }, (_, i) => `<i>${i.toString(16).toUpperCase()}</i>`).join("") + '</span><span class="asc">Text</span>' });
  const host = h("div.list-host");
  const foot = h("div.pane-foot.mono");
  body.append(head, host, foot);

  function render(el2, i) {
    if (!region) return;
    const base = region.start + i * 16;
    let hx = "", asc = "";
    const pg = page(base - (base % PAGE));
    for (let k = 0; k < 16; k++) {
      const a = base + k;
      const v = pg && pg !== "wait" ? pg[a % PAGE] : null;
      const p = prevByte(a);
      const cls = (a === sel ? " s" : "") + (p !== undefined && v !== null && p !== v ? " c" : "") + (v === 0 ? " z" : "");
      if (v === null) { hx += `<i class="u${cls}" data-o="${k}">${pg === "wait" ? "··" : "--"}</i>`; asc += `<i class="u${cls}" data-o="${k}"> </i>`; continue; }
      hx += `<i class="${cls.trim()}" data-o="${k}">${v.toString(16).toUpperCase().padStart(2, "0")}</i>`;
      const ch = v >= 0x20 && v < 0x7f ? String.fromCharCode(v) : ".";
      asc += `<i class="${cls.trim()}${v >= 0x20 && v < 0x7f ? "" : " np"}" data-o="${k}">${ch === "<" ? "&lt;" : ch === "&" ? "&amp;" : ch}</i>`;
    }
    el2.className = "vl-row mem";
    el2.innerHTML = `<span class="a">${hex(base)}</span><span class="hx">${hx}</span><span class="asc">${asc}</span>`;
  }
  const list = new VList(host, { rowHeight: 20, render });

  function showFoot() {
    if (sel == null) { foot.textContent = region ? `${region.name} · ${hex(region.start)}–${hex(region.start + region.size - 1)}` : ""; return; }
    const b = [0, 1, 2, 3].map((k) => byteAt(sel + k));
    const ok = (n) => b.slice(0, n).every((x) => x !== null && x !== undefined);
    const u16 = ok(2) ? (b[0] << 8) | b[1] : null;
    const u32 = ok(4) ? ((b[0] << 24) | (b[1] << 16) | (b[2] << 8) | b[3]) >>> 0 : null;
    foot.innerHTML = `<b>${hex(sel)}</b>` +
      (ok(1) ? `<span>u8 ${b[0]}</span>` : "") +
      (u16 !== null ? `<span>u16 ${hex(u16, 4)}</span>` : "") +
      (u32 !== null ? `<span>u32 ${hex(u32)}</span><span>i32 ${u32 | 0}</span>` : "") +
      `<span class="dim">big-endian</span>`;
  }

  list.onSelect = (i, ev) => {
    last = view;
    const cell = ev?.target?.closest?.("[data-o]");
    if (cell && region) { sel = region.start + i * 16 + Number(cell.dataset.o); list.invalidate(); showFoot(); }
  };
  let saveT = 0;
  list.onScroll = () => {
    clearTimeout(saveT);
    saveT = setTimeout(() => region && ctx.save({ addr: region.start + list.top * 16 }), 400);
  };

  const view = {
    list,
    reload() {
      regionSel.textContent = "";
      for (const r of source.regions) regionSel.append(h("option", { text: `${r.name}  ${hex(r.start)}` }));
      badge.hidden = !isSample;
      region = null;
      const want = ctx.params.addr ?? source.regions[0]?.start;
      if (want == null || !view.goTo(want, "top")) list.setCount(0);
      head.hidden = !region;
    },
    goTo(addr, how = "center") {
      const r = source.regions.find((x) => addr >= x.start && addr < x.start + x.size);
      if (!r) return false;
      if (r !== region) {
        region = r;
        regionSel.selectedIndex = source.regions.indexOf(r);
        list.setCount(Math.ceil(r.size / 16));
      }
      sel = addr >>> 0;
      list.select(Math.floor((addr - r.start) / 16), how);
      showFoot();
      return true;
    },
    followNow() {
      const v = registerValues()[follow];
      if (v !== undefined) view.goTo(v);
    },
  };
  const unsub = onRegisters(() => { if (follow) view.followNow(); list.invalidate(); showFoot(); });
  views.add(view);
  if (!last) last = view;
  view.reload();
  el.addEventListener("focusin", () => { last = view; });

  return {
    element: el,
    focus: () => el.focus({ preventScroll: true }),
    dispose: () => { views.delete(view); unsub(); list.dispose(); if (last === view) last = [...views][0] || null; },
    keydown(e) {
      if (e.target.tagName === "INPUT" || e.target.tagName === "SELECT") return false;
      if (!region) return false;
      const step = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -16, ArrowDown: 16 }[e.key];
      if (step !== undefined && sel != null) {
        const a = Math.max(region.start, Math.min(region.start + region.size - 1, sel + step));
        sel = a;
        list.select(Math.floor((a - region.start) / 16));
        showFoot();
        return true;
      }
      if (list.keydown(e)) {
        if (list.sel >= 0) { sel = region.start + list.sel * 16 + ((sel ?? 0) % 16); showFoot(); list.invalidate(); }
        return true;
      }
      if (e.key === "g") { goField.focus(); goField.select(); return true; }
      return false;
    },
  };
}
