// The fx-CG50 keypad: each key's label, its matrix position [row, col] (0-based, as in
// re/KEYMAP.md; the same table as the Android app's KeyMap.kt), the SHIFT legend (amber,
// above left) and the ALPHA legend (rose, above right).
const k = (label, r, c, shift = "", alpha = "", cls = "") => ({ label, r, c, shift, alpha, cls });

export const FKEYS = [
  k("F1", 6, 9, "Trace", "", "fn"), k("F2", 5, 9, "Zoom", "", "fn"), k("F3", 4, 9, "V-Win", "", "fn"),
  k("F4", 3, 9, "Sketch", "", "fn"), k("F5", 2, 9, "G-Solv", "", "fn"), k("F6", 1, 9, "G↔T", "", "fn"),
];

export const TOP_ROWS = [
  [k("SHIFT", 6, 8, "", "", "small"), k("OPTN", 5, 8, "", "", "small"), k("VARS", 4, 8, "PRGM", "", "small"), k("MENU", 3, 8, "SET UP", "", "small")],
  [k("ALPHA", 6, 7, "A-LOCK", "", "small"), k("x²", 5, 7, "√", "r"), k("^", 4, 7, "ˣ√", "θ"), k("EXIT", 3, 7, "QUIT", "", "small")],
];

export const DPAD = { up: [1, 8], down: [2, 7], left: [2, 8], right: [1, 7] };

export const FUNC_ROWS = [
  [k("X,θ,T", 6, 6, "∠", "A", "small"), k("log", 5, 6, "10ˣ", "B"), k("ln", 4, 6, "eˣ", "C"),
   k("sin", 3, 6, "sin⁻¹", "D"), k("cos", 2, 6, "cos⁻¹", "E"), k("tan", 1, 6, "tan⁻¹", "F")],
  [k("a⁄b", 6, 5, "ab⁄c", "G"), k("S⇔D", 5, 5, "F⇔D", "H", "small"), k("(", 4, 5, "∛", "I"),
   k(")", 3, 5, "x⁻¹", "J"), k(",", 2, 5, "", "K"), k("→", 1, 5, "", "L")],
];

export const NUM_ROWS = [
  [k("7", 6, 4, "CAPTURE", "M", "light"), k("8", 5, 4, "CLIP", "N", "light"), k("9", 4, 4, "PASTE", "O", "light"),
   k("DEL", 3, 4, "INS", "", "warm"), k("AC/ON", 0, 0, "OFF", "", "warm small")],
  [k("4", 6, 3, "CATALOG", "P", "light"), k("5", 5, 3, "FORMAT", "Q", "light"), k("6", 4, 3, "", "R", "light"),
   k("×", 3, 3, "{", "S", "light"), k("÷", 2, 3, "}", "T", "light")],
  [k("1", 6, 2, "List", "U", "light"), k("2", 5, 2, "Mat", "V", "light"), k("3", 4, 2, "", "W", "light"),
   k("+", 3, 2, "[", "X", "light"), k("−", 2, 2, "]", "Y", "light")],
  [k("0", 6, 1, "i", "Z", "light"), k(".", 5, 1, "=", "SPACE", "light"), k("×10ˣ", 4, 1, "π", "\"", "light small"),
   k("(−)", 3, 1, "Ans", "", "light"), k("EXE", 2, 1, "↵", "", "light small")],
];

// build fills el with the keypad; onDown/onUp get (row, col).
export function build(el, onDown, onUp) {
  const keyEl = (key) => {
    const cell = document.createElement("div");
    cell.className = "kcell";
    const leg = document.createElement("div");
    leg.className = "legend";
    leg.innerHTML = `<span class="sh"></span><span class="al"></span>`;
    leg.children[0].textContent = key.shift;
    leg.children[1].textContent = key.alpha;
    const b = document.createElement("button");
    b.className = ("key " + key.cls).trim();
    b.textContent = key.label;
    wire(b, key.r, key.c);
    cell.append(leg, b);
    return cell;
  };
  const wire = (b, r, c) => {
    b.dataset.rc = `${r},${c}`;
    b.tabIndex = -1;
    b.addEventListener("pointerdown", (e) => { b.setPointerCapture(e.pointerId); b.classList.add("down"); onDown(r, c); });
    const up = () => { if (b.classList.contains("down")) { b.classList.remove("down"); onUp(r, c); } };
    b.addEventListener("pointerup", up);
    b.addEventListener("pointercancel", up);
  };
  const row = (keys) => {
    const d = document.createElement("div");
    d.className = "krow";
    keys.forEach((key) => d.append(keyEl(key)));
    return d;
  };
  const gap = () => Object.assign(document.createElement("div"), { className: "gap" });

  el.append(row(FKEYS), gap());

  const top = document.createElement("div");
  top.className = "topblock";
  const rows = document.createElement("div");
  rows.className = "rows";
  TOP_ROWS.forEach((r) => rows.append(row(r)));
  const pad = document.createElement("div");
  pad.className = "dpad";
  for (const [dir, glyph, cls] of [["up", "▲", "up"], ["down", "▼", "down-k"], ["left", "◀", "left"], ["right", "▶", "right"]]) {
    const b = document.createElement("button");
    b.className = cls;
    b.textContent = glyph;
    wire(b, DPAD[dir][0], DPAD[dir][1]);
    pad.append(b);
  }
  top.append(rows, pad);
  el.append(top, gap());

  FUNC_ROWS.forEach((r) => el.append(row(r)));
  el.append(gap());
  NUM_ROWS.forEach((r) => el.append(row(r)));
}

// show marks the on-screen key at (row, col) pressed or released (for the physical keyboard).
export function show(el, r, c, down) {
  el.querySelectorAll(`[data-rc="${r},${c}"]`).forEach((b) => b.classList.toggle("down", down));
}
