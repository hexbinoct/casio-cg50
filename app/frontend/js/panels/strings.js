// The Strings window: search the text in memory (ASCII, Casio's multibyte, UTF-16); a
// result opens in a memory view.
//
// Interface:
//   setStrings(provider)   provider = {
//       search(query, { encoding: "any" | "ascii" | "casio" | "utf16", minLength })
//         → Promise<[{ addr, text, encoding }]>     ("" lists everything, up to the provider) }
import { VList, esc, hex } from "../vlist.js";
import { pane, h, field, sampleBadge } from "./pane.js";
import { nav } from "../nav.js";

export const meta = { kind: "strings", title: "Strings", multi: false };

let provider = null;
let isSample = false;
const views = new Set();

export function setStrings(p, { sample = false } = {}) {
  provider = p;
  isSample = sample;
  for (const v of views) v.search();
}

const ENC = { any: "Any encoding", ascii: "ASCII", casio: "Casio", utf16: "UTF-16" };
const ENC_SHORT = { ascii: "ascii", casio: "casio", utf16: "utf-16" };

export function create(ctx) {
  const { el, tools, body } = pane("strings");
  let results = [];
  let q = ctx.params.q || "";
  const box = field("Search text", () => view.search(), { width: 180 });
  box.value = q;
  box.classList.add("search");
  let t = 0;
  box.addEventListener("input", () => { clearTimeout(t); t = setTimeout(() => view.search(), 120); });
  const enc = h("select.field", { title: "Encoding" });
  for (const [k, v] of Object.entries(ENC)) enc.append(h("option", { value: k, text: v }));
  enc.value = ENC[ctx.params.enc] ? ctx.params.enc : "any";
  enc.addEventListener("change", () => view.search());
  const count = h("span.where");
  const badge = sampleBadge();
  tools.append(box, enc, h("span.grow"), count, badge);

  const host = h("div.list-host");
  body.append(h("div.list-head.str", { html: '<span class="a">Address</span><span class="e">Kind</span><span class="t">Text</span>' }), host);
  const list = new VList(host, {
    rowHeight: 20,
    render(row, i) {
      const r = results[i];
      row.className = "vl-row str";
      let text = esc(r.text);
      if (q) {
        const at = r.text.toLowerCase().indexOf(q.toLowerCase());
        if (at >= 0) text = esc(r.text.slice(0, at)) + "<mark>" + esc(r.text.slice(at, at + q.length)) + "</mark>" + esc(r.text.slice(at + q.length));
      }
      row.innerHTML = `<span class="a">${hex(r.addr)}</span><span class="e">${ENC_SHORT[r.encoding] || ""}</span><span class="t">${text}</span>`;
    },
  });
  list.onSelect = (i, ev) => { if (ev) nav.data(results[i].addr); };
  list.onActivate = (i) => nav.data(results[i].addr);

  let seq = 0;
  const view = {
    async search() {
      badge.hidden = !isSample;
      q = box.value;
      ctx.save({ q, enc: enc.value });
      if (!provider) { results = []; list.setCount(0); count.textContent = ""; return; }
      const my = ++seq;
      const r = await provider.search(q, { encoding: enc.value, minLength: 4 });
      if (my !== seq) return; // a newer search started
      results = r;
      count.textContent = `${r.length.toLocaleString()} found`;
      list.sel = -1;
      list.setCount(r.length);
      list.scrollTo(0, "top");
    },
  };
  views.add(view);
  view.search();
  return {
    element: el,
    focus: () => box.focus(),
    dispose: () => { views.delete(view); list.dispose(); },
    keydown: (e) => (e.target.tagName === "INPUT" ? false : list.keydown(e)),
  };
}
