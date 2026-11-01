// Links between windows: "show this address as code", "show it as data", "write to the
// console". The workspace (workspace.js) fills these in, opening the window if needed, so a
// window never has to know where the others are.

export const nav = {
  code: (addr) => {},   // disassembly at addr (opens one)
  data: (addr) => {},   // the last used memory view at addr (opens one)
  open: (kind) => {},   // open or focus a window kind
  log: (kind, text) => {}, // console
  layout: (name) => false, // switch to a preset layout ("calculator", "debugging", "re")
};
