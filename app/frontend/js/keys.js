// The computer keyboard → calculator keys. Keys in HOLD are held for as long as the computer
// key is (games, key repeat); a letter types ALPHA + that letter as two quick presses.
const ALPHA = [6, 7];

export const HOLD = {
  "0": [6, 1], "1": [6, 2], "2": [5, 2], "3": [4, 2], "4": [6, 3], "5": [5, 3], "6": [4, 3],
  "7": [6, 4], "8": [5, 4], "9": [4, 4], ".": [5, 1],
  "+": [3, 2], "-": [2, 2], "*": [3, 3], "/": [2, 3], "^": [4, 7],
  "(": [4, 5], ")": [3, 5], ",": [2, 5],
  Enter: [2, 1], Backspace: [3, 4], Delete: [3, 4],
  Escape: [3, 7], Home: [3, 8], End: [0, 0], PageUp: [5, 8], PageDown: [4, 8],
  Tab: [6, 8], "`": [6, 7],
  ArrowUp: [1, 8], ArrowDown: [2, 7], ArrowLeft: [2, 8], ArrowRight: [1, 7],
  F1: [6, 9], F2: [5, 9], F3: [4, 9], F4: [3, 9], F5: [2, 9], F6: [1, 9],
};

const LETTER = {
  a: [6, 6], b: [5, 6], c: [4, 6], d: [3, 6], e: [2, 6], f: [1, 6],
  g: [6, 5], h: [5, 5], i: [4, 5], j: [3, 5], k: [2, 5], l: [1, 5],
  m: [6, 4], n: [5, 4], o: [4, 4], p: [6, 3], q: [5, 3], r: [4, 3],
  s: [3, 3], t: [2, 3], u: [6, 2], v: [5, 2], w: [4, 2], x: [3, 2],
  y: [2, 2], z: [6, 1],
};

// letter returns the ALPHA + letter presses for a single letter key, else null.
export function letter(key) {
  if (key.length !== 1) return null;
  const at = LETTER[key.toLowerCase()];
  return at ? [ALPHA, at] : null;
}

// For the keyboard help dialog.
export const HELP = [
  ["0–9  .  + − × ÷  ^  ( )  ,", "the same keys"],
  ["Enter", "EXE"], ["Backspace / Delete", "DEL"],
  ["Esc", "EXIT"], ["Home", "MENU"],
  ["End", "AC/ON"], ["Arrows", "the D-pad"],
  ["F1–F6", "F1–F6"], ["Tab", "SHIFT"],
  ["`", "ALPHA"], ["a–z", "ALPHA + that letter"],
  ["Page Up", "OPTN"], ["Page Down", "VARS"],
  ["F8", "run / pause"], ["Ctrl+S / Ctrl+L", "save / load state (slot 1)"],
  ["Ctrl+O", "open a flash dump"], ["Ctrl+I", "install add-ins"],
  ["Ctrl+P", "screenshot"], ["Ctrl+R", "restart the calculator"],
];
