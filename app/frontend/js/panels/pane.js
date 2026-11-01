// Building blocks shared by the windows: a pane = an optional toolbar over a body.

// pane returns { el, tools, body }; sample: true adds the "sample data" badge to the toolbar.
export function pane(kind, { tools = true, sample = false } = {}) {
  const el = document.createElement("div");
  el.className = "pane pane-" + kind;
  el.tabIndex = 0; // the window takes keyboard focus when clicked (app.js routes keys by focus)
  let bar = null;
  if (tools) {
    bar = document.createElement("div");
    bar.className = "pane-tools";
    el.append(bar);
  }
  const body = document.createElement("div");
  body.className = "pane-body";
  el.append(body);
  if (sample && bar) bar.append(sampleBadge());
  return { el, tools: bar, body };
}

export function sampleBadge() {
  const b = document.createElement("span");
  b.className = "badge sample";
  b.textContent = "sample";
  b.title = "Sample data: this window isn't connected to the debug engine yet";
  return b;
}

// h builds an element: h("button.btn.icon", { title: "x", onclick }, child, …).
export function h(spec, props = {}, ...kids) {
  const [tag, ...cls] = spec.split(".");
  const el = document.createElement(tag || "div");
  if (cls.length) el.className = cls.join(" ");
  for (const [k, v] of Object.entries(props || {})) {
    if (k === "dataset") Object.assign(el.dataset, v);
    else if (k in el && typeof v !== "string") el[k] = v;
    else if (k === "text") el.textContent = v;
    else if (k === "html") el.innerHTML = v;
    else el.setAttribute(k, v);
  }
  for (const c of kids) if (c != null) el.append(c);
  return el;
}

export function icon(name, size = 16) {
  const s = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  s.setAttribute("width", size);
  s.setAttribute("height", size);
  const u = document.createElementNS("http://www.w3.org/2000/svg", "use");
  u.setAttribute("href", "#i-" + name);
  s.append(u);
  return s;
}

export function toolButton(iconName, title, onclick) {
  const b = h("button.tbtn", { title, onclick });
  b.append(icon(iconName, 15));
  return b;
}

// field is a compact text input for a toolbar (Enter calls onEnter with its value).
export function field(placeholder, onEnter, { width = 0, title = "" } = {}) {
  const i = h("input.field", { placeholder, spellcheck: false, title: title || placeholder });
  i.autocomplete = "off";
  if (width) i.style.width = width + "px";
  i.addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); onEnter(i.value, e); }
    if (e.key === "Escape") { i.blur(); i.closest(".pane")?.focus(); }
  });
  return i;
}
