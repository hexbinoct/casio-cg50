// Paged: rows of a list that lives in Go (the disassembly's millions of rows, a string
// search's results), fetched in blocks and cached. A list's render asks get(i) for the rows in
// view; a row not here yet returns undefined and its block is requested. Requests wait a
// moment (so a fast scroll asks only for where it stops), at most two are in flight, and a
// block nobody asked for in the last half second is not fetched at all. When a block arrives,
// onLoad() runs (the list redraws). The newest blocks are kept, up to maxBlocks.
//
//   const p = new Paged((first, n) => go().DisasmRows(space, first, n), { onLoad: () => list.invalidate() });
//   p.get(i) → row | undefined;  p.clear()  (after a stop: the memory may have changed)

export class Paged {
  constructor(fetch, { block = 256, maxBlocks = 200, onLoad = () => {} } = {}) {
    this.fetch = fetch;
    this.block = block;
    this.max = maxBlocks;
    this.onLoad = onLoad;
    this.blocks = new Map(); // block number → rows (in use order)
    this.wanted = new Map(); // block number → when it was last asked for
    this.flying = new Set();
    this.timer = 0;
    this.gen = 0;
  }

  get(i) {
    const b = Math.floor(i / this.block);
    const rows = this.blocks.get(b);
    if (rows) {
      this.blocks.delete(b);
      this.blocks.set(b, rows); // most recently used last
      return rows[i - b * this.block];
    }
    this.wanted.set(b, performance.now());
    this.schedule();
    return undefined;
  }

  // has(i) tells whether row i is here (without asking for it).
  has(i) { return this.blocks.has(Math.floor(i / this.block)); }

  clear() {
    this.blocks.clear();
    this.wanted.clear();
    this.gen++;
  }

  schedule() {
    if (this.timer) return;
    this.timer = setTimeout(() => { this.timer = 0; this.flush(); }, 16);
  }

  flush() {
    const now = performance.now();
    for (const [b, t] of this.wanted) if (now - t > 500) this.wanted.delete(b);
    // newest wishes first: they are where the list is now
    const order = [...this.wanted.entries()].filter(([b]) => !this.flying.has(b)).sort((x, y) => y[1] - x[1]);
    for (const [b] of order) {
      if (this.flying.size >= 2) break;
      this.wanted.delete(b);
      this.load(b);
    }
  }

  async load(b) {
    this.flying.add(b);
    const gen = this.gen;
    let rows = null;
    try { rows = await this.fetch(b * this.block, this.block); } catch (_) { rows = null; }
    this.flying.delete(b);
    if (gen === this.gen && rows) {
      this.blocks.set(b, rows);
      while (this.blocks.size > this.max) this.blocks.delete(this.blocks.keys().next().value);
      this.onLoad();
    }
    if (this.wanted.size) this.flush();
  }
}
