// The Strings window: search the text in a region of memory (ASCII, Casio's multibyte,
// UTF-16) found by Go's scanner; a result opens in a memory view (double-click: as code).
// The results stay in Go and come in pages (paged.js), so a search over the whole flash
// with tens of thousands of matches is still one quick call.
//
// Interface:
//   setStrings(provider)   provider = {
//       regions: [{ id, name }],
//       search(region, query, { encoding, minLength }) → Promise<{ count, total, fetch(first, n) → Promise<rows> }>
//       row = { addr, text, encoding } }
import { VList, esc, hex } from "../vlist.js";
import { Paged } from "../paged.js";
import { pane, h, field } from "./pane.js";
import { nav } from "../nav.js";

export const meta = { kind: "strings", title: "Strings", multi: false };

let provider = null;
const views = new Set();

export function setStrings(p) {
  provider = p;
  for (const v of views) v.reload();
}

// search(q): the Strings window searches for q (the console's "strings" command).
export function search(q) { for (const v of views) v.searchFor(q); }

const ENC = { any: "Any encoding", ascii: "ASCII", casio: "Casio", utf16: "UTF-16" };
const ENC_SHORT = { ascii: "ascii", casio: "casio", utf16: "utf-16" };

export function create(ctx) {
  const { el, tools, body } = pane("strings");
  let q = ctx.params.q || "";
  let res = null, paged = null;
  const regionSel = h("select.field", { title: "Where to look" });
  regionSel.addEventListener("change", () => view.search());
  const box = field("Search text", () => view.search(), { width: 180 });
  box.value = q;
  box.classList.add("search");
  let t = 0;
  box.addEventListener("input", () => { clearTimeout(t); t = setTimeout(() => view.search(), 160); });
  const enc = h("select.field", { title: "Encoding" });
  for (const [k, v] of Object.entries(ENC)) enc.append(h("option", { value: k, text: v }));
  enc.value = ENC[ctx.params.enc] ? ctx.params.enc : "any";
  enc.addEventListener("change", () => view.search());
  const count = h("span.where");
  tools.append(regionSel, box, enc, h("span.grow"), count);

  const host = h("div.list-host");
  const empty = h("div.empty", { text: "Open a flash dump to search its text." });
  body.append(h("div.list-head.str", { html: '<span class="a">Address</span><span class="e">Kind</span><span class="t">Text</span>' }), host, empty);
  const list = new VList(host, {
    rowHeight: 20,
    render(row, i) {
      const r = paged?.get(i);
      row.className = "vl-row str";
      if (!r) { row.innerHTML = '<span class="a dim">········</span>'; return; }
      let text = esc(r.text);
      if (q) {
        const at = r.text.toLowerCase().indexOf(q.toLowerCase());
        if (at >= 0) text = esc(r.text.slice(0, at)) + "<mark>" + esc(r.text.slice(at, at + q.length)) + "</mark>" + esc(r.text.slice(at + q.length));
      }
      row.innerHTML = `<span class="a">${hex(r.addr)}</span><span class="e">${ENC_SHORT[r.encoding] || ""}</span><span class="t">${text}</span>`;
    },
  });
  const rowAt = (i) => paged?.get(i);
  list.onSelect = (i, ev) => { if (ev && rowAt(i)) nav.data(rowAt(i).addr); };
  list.onActivate = (i, ev) => { const r = rowAt(i); if (r) (ev?.type === "dblclick" ? nav.code : nav.data)(r.addr); };

  let seq = 0;
  const view = {
    reload() {
      const keep = regionSel.value || ctx.params.region || "os";
      regionSel.textContent = "";
      for (const r of provider?.regions || []) regionSel.append(h("option", { value: r.id, text: r.name }));
      if ([...regionSel.options].some((o) => o.value === keep)) regionSel.value = keep;
      view.search();
    },
    searchFor(text) { box.value = text; view.search(); },
    async search() {
      q = box.value;
      ctx.save({ q, enc: enc.value, region: regionSel.value });
      empty.hidden = !!provider;
      if (!provider) { paged = null; list.setCount(0); count.textContent = ""; return; }
      const my = ++seq;
      count.textContent = "searching…";
      let r;
      try { r = await provider.search(regionSel.value, q, { encoding: enc.value, minLength: 4 }); }
      catch (err) { if (my === seq) { count.textContent = String(err).replace(/^Error: /, ""); paged = null; list.setCount(0); } return; }
      if (my !== seq) return; // a newer search started
      res = r;
      paged = new Paged((first, n) => res.fetch(first, n), { block: 200, onLoad: () => list.invalidate() });
      count.textContent = `${r.count.toLocaleString()} of ${r.total.toLocaleString()} strings`;
      list.sel = -1;
      list.setCount(r.count);
      list.scrollTo(0, "top");
    },
  };
  views.add(view);
  view.reload();
  return {
    element: el,
    focus: () => box.focus(),
    dispose: () => { views.delete(view); list.dispose(); },
    keydown: (e) => (e.target.tagName === "INPUT" || e.target.tagName === "SELECT" ? false : list.keydown(e)),
  };
}
