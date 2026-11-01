// The Console window: what the app reports (the same messages as the toasts, kept), what the
// debugger says (every stop), and a command line: help lists the commands; engine.js adds
// the debugger's (run, step, registers, memory, breakpoints, …).
//
// Interface:
//   log(kind, text)                 kind: "info" | "warn" | "error" | "out"
//   addCommand(name, help, fn)      fn(args: string[], print(text)) for the command line (may be async)
import { pane, h, toolButton } from "./pane.js";
import { esc, parseAddr } from "../vlist.js";
import { nav } from "../nav.js";
import * as dbg from "../debugger.js";
import { findName } from "./disasm.js";

export const meta = { kind: "console", title: "Console", multi: false };

const MAX = 2000;
const lines = []; // { t, kind, text }
const views = new Set();
const commands = new Map();

export function log(kind, text) {
  const l = { t: new Date(), kind, text: String(text) };
  lines.push(l);
  if (lines.length > MAX) lines.shift();
  for (const v of views) v.add(l);
}

export function addCommand(name, help, fn) { commands.set(name, { help, fn }); }

addCommand("help", "this list", (_, print) => {
  for (const [n, c] of commands) print(`${n.padEnd(14)} ${c.help}`);
});
addCommand("clear", "empty the console", () => { lines.length = 0; for (const v of views) v.clearAll(); });
addCommand("g", "<address|name>  show it in the disassembly", async ([a], print) => {
  const x = a ? await findName(a) : null;
  if (x == null) print("g: an address or a function's name, e.g. g 80001234, g GetKey"); else nav.code(x);
});
addCommand("m", "<address>  show it in a memory view", ([a], print) => {
  const x = parseAddr(a || "");
  if (x == null) print("m: an address, e.g. m 8C000000"); else nav.data(x);
});
addCommand("bp", "<address|name> [read|write|access [length]]  add a breakpoint or watchpoint", async ([a, k, n], print) => {
  const x = a ? await findName(a) : null;
  if (x == null) { print("bp: an address or a function's name"); return; }
  const kind = ["read", "write", "access"].includes(k) ? k : "exec";
  const b = dbg.addBreakpoint(x, kind, kind === "exec" ? {} : { len: Math.max(1, Number(n) || 1) });
  print(`breakpoint ${b.id}: ${kind} at ${x.toString(16).toUpperCase().padStart(8, "0")}`);
});
addCommand("bc", "remove every breakpoint", () => dbg.clearBreakpoints());
addCommand("layout", "<calculator|debugging|re>  switch the workspace layout", ([n], print) => {
  if (!nav.layout(n)) print("layout: calculator, debugging or re");
});

function time(d) { return d.toTimeString().slice(0, 8); }

export function create() {
  const { el, tools, body } = pane("console");
  tools.append(h("span.where", { text: "Type help for the commands" }), h("span.grow"), toolButton("trash", "Clear", () => commands.get("clear").fn([], () => {})));
  const out = h("div.con-out.scroll");
  const input = h("input.con-in", { placeholder: "›  command", spellcheck: false });
  input.autocomplete = "off";
  body.append(out, input);
  const hist = [];
  let hi = 0;
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && input.value.trim()) {
      const text = input.value.trim();
      hist.push(text); hi = hist.length;
      input.value = "";
      log("cmd", text);
      const [name, ...args] = text.split(/\s+/);
      const c = commands.get(name);
      const print = (t) => log("out", t);
      if (!c) print(`${name}: no such command (help lists them)`);
      else Promise.resolve().then(() => c.fn(args, print)).catch((err) => log("error", String(err)));
    } else if (e.key === "ArrowUp" && hi > 0) { input.value = hist[--hi]; e.preventDefault(); }
    else if (e.key === "ArrowDown" && hi < hist.length) { input.value = hist[++hi] ?? ""; e.preventDefault(); }
  });
  out.addEventListener("click", () => { if (!getSelection().toString()) input.focus(); });

  const line = (l) => h("div.con-line.k-" + l.kind, { html: `<span class="t">${time(l.t)}</span><span class="x">${l.kind === "cmd" ? "› " : ""}${esc(l.text)}</span>` });
  const view = {
    add(l) {
      const stick = out.scrollTop + out.clientHeight >= out.scrollHeight - 4;
      out.append(line(l));
      if (out.childElementCount > MAX) out.firstElementChild.remove();
      if (stick) out.scrollTop = out.scrollHeight;
    },
    clearAll() { out.textContent = ""; },
  };
  for (const l of lines) out.append(line(l));
  views.add(view);
  requestAnimationFrame(() => { out.scrollTop = out.scrollHeight; });
  return { element: el, focus: () => input.focus(), dispose: () => views.delete(view) };
}
