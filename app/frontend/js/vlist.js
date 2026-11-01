// VList: a virtual list of fixed-height rows for any number of rows (the disassembly of a
// whole 32 MB flash is 17 million). Only the rows in view exist in the DOM: a small pool of
// row elements is reused as it scrolls, and a row is redrawn only when the index it shows
// changes or the list is invalidated. The scroll position is a row number (a float) and the
// scroll bar is our own, because browsers cap an element's height at a few million pixels.
//
//   const list = new VList(host, { rowHeight: 20, count: n, render: (el, i) => { ... } });
//   list.setCount(n); list.invalidate(); list.scrollTo(i, "center"); list.select(i);
//   list.onSelect = (i, ev) => {}; list.onActivate = (i, ev) => {};   // click / double-click, Enter
//   list.keydown(ev) → true when it handled a navigation key

export class VList {
  constructor(host, { rowHeight = 20, count = 0, render, className = "" } = {}) {
    this.host = host;
    this.rh = rowHeight;
    this.count = count;
    this.render = render;
    this.pos = 0; // first visible row, fractional
    this.sel = -1;
    this.onSelect = null;
    this.onActivate = null;
    this.onScroll = null;
    this.pool = [];
    this.dirty = true;
    this.viewH = 0;
    this.queued = false;

    host.classList.add("vlist");
    if (className) host.classList.add(...className.split(" "));
    this.rows = document.createElement("div");
    this.rows.className = "vl-rows";
    this.bar = document.createElement("div");
    this.bar.className = "vl-bar";
    this.thumb = document.createElement("div");
    this.thumb.className = "vl-thumb";
    this.bar.append(this.thumb);
    host.append(this.rows, this.bar);

    host.addEventListener("wheel", (e) => this.wheel(e), { passive: false });
    this.rows.addEventListener("click", (e) => this.click(e, false));
    this.rows.addEventListener("dblclick", (e) => this.click(e, true));
    this.wireBar();
    this.ro = new ResizeObserver(() => this.measure());
    this.ro.observe(host);
  }

  dispose() { this.ro.disconnect(); }

  get visible() { return Math.max(1, Math.floor(this.viewH / this.rh)); }
  get maxPos() { return Math.max(0, this.count - this.visible); }
  get top() { return Math.floor(this.pos); }

  setCount(n) {
    this.count = n;
    if (this.sel >= n) this.sel = n - 1;
    this.pos = Math.min(this.pos, this.maxPos);
    this.invalidate();
  }

  invalidate() { this.dirty = true; this.schedule(); }

  setPos(p) {
    p = Math.max(0, Math.min(this.maxPos, p));
    if (p === this.pos) return;
    this.pos = p;
    this.schedule();
    this.onScroll?.(this.pos);
  }

  // scrollTo brings row i into view: "top", "center", or "nearest" (only if it's outside).
  scrollTo(i, how = "nearest") {
    const v = this.visible;
    if (how === "top") this.setPos(i);
    else if (how === "center") this.setPos(i - Math.floor(v / 2));
    else if (i < this.pos) this.setPos(i);
    else if (i > this.pos + v - 1) this.setPos(i - v + 1);
  }

  select(i, how = "nearest", ev = null) {
    if (this.count === 0) return;
    i = Math.max(0, Math.min(this.count - 1, i));
    this.sel = i;
    this.scrollTo(i, how);
    this.invalidate();
    this.onSelect?.(i, ev);
  }

  keydown(e) {
    const v = this.visible;
    const step = { ArrowUp: -1, ArrowDown: 1, PageUp: -v + 1, PageDown: v - 1 }[e.key];
    if (step !== undefined && !e.altKey && !e.metaKey) {
      const from = this.sel >= 0 && this.sel >= this.top && this.sel < this.top + v ? this.sel : this.top;
      this.select(from + step, "nearest", e);
      return true;
    }
    if (e.key === "Home" && (e.ctrlKey || e.metaKey)) { this.select(0, "top", e); return true; }
    if (e.key === "End" && (e.ctrlKey || e.metaKey)) { this.select(this.count - 1, "nearest", e); return true; }
    if (e.key === "Enter" && this.sel >= 0) { this.onActivate?.(this.sel, e); return true; }
    return false;
  }

  // ---- internals ---------------------------------------------------------------------

  measure() {
    const h = this.host.clientHeight;
    if (h === this.viewH && this.pool.length) return;
    this.viewH = h;
    const need = Math.ceil(h / this.rh) + 2;
    while (this.pool.length < need) {
      const el = document.createElement("div");
      el.className = "vl-row";
      el.style.height = this.rh + "px";
      el.style.transform = `translateY(${this.pool.length * this.rh}px)`;
      el._i = -1;
      this.rows.append(el);
      this.pool.push(el);
    }
    this.pos = Math.min(this.pos, this.maxPos);
    this.invalidate();
  }

  schedule() {
    if (this.queued) return;
    this.queued = true;
    requestAnimationFrame(() => { this.queued = false; this.draw(); });
  }

  draw() {
    const top = this.top;
    const frac = this.pos - top;
    this.rows.style.transform = `translateY(${-frac * this.rh}px)`;
    for (let k = 0; k < this.pool.length; k++) {
      const el = this.pool[k];
      const i = top + k;
      if (i >= this.count) {
        if (el._i !== -1) { el._i = -1; el.hidden = true; }
        continue;
      }
      if (el.hidden) el.hidden = false;
      if (el._i !== i || this.dirty) {
        el._i = i;
        this.render(el, i);
      }
      el.classList.toggle("sel", i === this.sel);
    }
    this.dirty = false;
    this.drawBar();
  }

  drawBar() {
    const track = this.viewH;
    const show = this.count > this.visible;
    this.bar.hidden = !show;
    if (!show) return;
    const th = Math.max(28, Math.min(track, (this.visible / this.count) * track));
    const y = this.maxPos ? (this.pos / this.maxPos) * (track - th) : 0;
    this.thumb.style.height = th + "px";
    this.thumb.style.transform = `translateY(${y}px)`;
    this.thumbH = th;
  }

  wheel(e) {
    if (e.ctrlKey) return; // pinch zoom
    e.preventDefault();
    let dy = e.deltaY;
    if (e.deltaMode === 1) dy *= this.rh;
    else if (e.deltaMode === 2) dy *= this.viewH;
    this.setPos(this.pos + dy / this.rh);
  }

  click(e, dbl) {
    const el = e.target.closest(".vl-row");
    if (!el || el._i < 0) return;
    if (dbl) this.onActivate?.(el._i, e);
    else this.select(el._i, "nearest", e);
  }

  wireBar() {
    let drag = null;
    this.thumb.addEventListener("pointerdown", (e) => {
      e.preventDefault();
      e.stopPropagation();
      this.thumb.setPointerCapture(e.pointerId);
      drag = { y: e.clientY, pos: this.pos };
      this.bar.classList.add("drag");
    });
    this.thumb.addEventListener("pointermove", (e) => {
      if (!drag) return;
      const span = this.viewH - this.thumbH;
      if (span > 0) this.setPos(drag.pos + ((e.clientY - drag.y) / span) * this.maxPos);
    });
    const end = () => { drag = null; this.bar.classList.remove("drag"); };
    this.thumb.addEventListener("pointerup", end);
    this.thumb.addEventListener("pointercancel", end);
    // a click on the track pages toward it
    this.bar.addEventListener("pointerdown", (e) => {
      if (e.target !== this.bar) return;
      e.preventDefault();
      const r = this.thumb.getBoundingClientRect();
      this.setPos(this.pos + (e.clientY < r.top ? -1 : 1) * (this.visible - 1));
    });
  }
}

// esc makes text safe for innerHTML.
export function esc(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
}

export const hex = (v, n = 8) => (v >>> 0).toString(16).toUpperCase().padStart(n, "0");

// parseAddr reads "8C001234", "0x8C001234", "8c00_1234", or "+40"/"-40" relative to base.
export function parseAddr(text, base = 0) {
  const t = String(text).trim().replace(/_/g, "");
  const rel = /^([+-])(?:0x)?([0-9a-f]+)$/i.exec(t);
  if (rel) return (base + (rel[1] === "-" ? -1 : 1) * parseInt(rel[2], 16)) >>> 0;
  const m = /^(?:0x)?([0-9a-f]{1,8})$/i.exec(t);
  return m ? parseInt(m[1], 16) >>> 0 : null;
}
