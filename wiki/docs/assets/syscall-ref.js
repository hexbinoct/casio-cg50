/* Syscall reference (os/syscall-reference.md): search and filter the 8,040 syscalls of OS 3.60.
   Data: syscalls360.json next to this file, built by re/syscall_ref.py. Plain JS, no libraries.
   Row layout (see re/syscall_ref.py):
   [a60, a80, cmp, size60, size80, flags, args, calls, mmio, cat, catVia, msg, (k)] */
(function () {
  "use strict";
  var script = document.currentScript;
  function start() {
    var app = document.getElementById("scr-app");
    if (!app || !script) return;
    var dataUrl = new URL("syscalls360.json", script.src).href;
    var root = new URL("../", script.src).href;
    app.innerHTML = '<p class="scr-loading">Loading the table…</p>';
    fetch(dataUrl)
      .then(function (r) {
        if (!r.ok) throw new Error("HTTP " + r.status);
        return r.json();
      })
      .then(function (d) { init(app, d, root); })
      .catch(function (e) {
        app.innerHTML = '<p class="scr-loading">Could not load the syscall data (' +
          esc(String(e.message || e)) + ").</p>";
      });
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start);
  else start();

  var WP = "https://prizm.cemetech.net/";
  var GINT = "https://git.planet-casio.com/Lephenixnoir/gint/src/branch/master/src/kernel/syscalls.S";
  var CMP = [
    ["same", "Same code in 3.80 (byte for byte)"],
    ["same, moved", "Same code in 3.80 once addresses in the literal pool and bsr offsets are masked"],
    ["changed", "The code differs in 3.80 (same size)"],
    ["changed", "The code differs in 3.80 (different size)"]
  ];
  var PAGE = 200;

  function esc(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function hex(v, w) {
    var s = (v >>> 0).toString(16).toUpperCase();
    while (s.length < w) s = "0" + s;
    return "0x" + s;
  }
  function num(n) { return hex(n, 4); }
  function addr(a) { return hex(a, 8); }

  function init(app, d, root) {
    var rows = d.rows, cats = d.cats, N = rows.length;
    var names = {};
    Object.keys(d.names).forEach(function (k) { names[+k] = d.names[k]; });

    // derived: aliases (several numbers, one address), callers, neighbours by address
    var byAddr = {};
    rows.forEach(function (r, i) { (byAddr[r[0]] = byAddr[r[0]] || []).push(i); });
    var callers = {};
    rows.forEach(function (r, i) {
      r[7].forEach(function (c) {
        if (c < 0x10000) (callers[c] = callers[c] || []).push(i);
      });
    });
    var named = [];
    for (var i = 0; i < N; i++) if (names[i] && names[i].n) named.push(i);
    named.sort(function (a, b) { return rows[a][0] - rows[b][0] || a - b; });
    var order = [];
    for (i = 0; i < N; i++) order.push(i);

    function nameOf(i) { return names[i] && names[i].n ? names[i].n : ""; }
    function neighbours(i) {
      var a = rows[i][0], lo = 0, hi = named.length;
      while (lo < hi) { var m = (lo + hi) >> 1; if (rows[named[m]][0] < a) lo = m + 1; else hi = m; }
      var before = lo > 0 ? named[lo - 1] : null, after = null;
      for (var j = lo; j < named.length; j++) if (rows[named[j]][0] > a) { after = named[j]; break; }
      return [before, after];
    }
    function argText(m) {
      if (m === 0) return "none";
      var hiReg = 0;
      for (var b = 0; b < 4; b++) if (m & (1 << b)) hiReg = b;
      return (hiReg + 1) + " (" + (hiReg === 0 ? "r4" : "r4–r" + (4 + hiReg)) + ")";
    }

    // ---- UI -------------------------------------------------------------------------------
    var catN = cats.map(function () { return 0; });
    rows.forEach(function (r) {
      for (var k = 0; k < cats.length; k++) if ((r[9] | r[10]) & (1 << k)) catN[k]++;
    });
    var catOpts = '<option value="">Any hardware (a guess)</option>' + cats.map(function (c, k) {
      return catN[k] ? '<option value="' + k + '">' + esc(c[1]) + "</option>" : "";
    }).join("");
    app.innerHTML =
      '<div class="scr-controls">' +
      '<input id="scr-q" type="search" autocomplete="off" spellcheck="false" ' +
      'placeholder="Number (0x1839 or 6201), name, message, or an address in 3.60 or 3.80" ' +
      'aria-label="Search syscalls">' +
      '<div class="scr-filters">' +
      '<label><input type="checkbox" id="scr-named"> Named only</label>' +
      '<label><input type="checkbox" id="scr-gint"> Used by gint</label>' +
      '<label><input type="checkbox" id="scr-lib"> In libfxcg</label>' +
      '<label><input type="checkbox" id="scr-wp"> On WikiPrizm</label>' +
      '<label><input type="checkbox" id="scr-changed"> Changed in 3.80</label>' +
      '<label><input type="checkbox" id="scr-stub"> Empty or trivial</label>' +
      '<select id="scr-cat" aria-label="Hardware category (a guess)">' + catOpts + "</select>" +
      '<label><input type="checkbox" id="scr-via"> incl. through calls</label>' +
      "</div></div>" +
      '<p class="scr-count" id="scr-count" aria-live="polite"></p>' +
      '<p class="scr-count" id="scr-note"></p>' +
      '<div class="scr-wrap"><table class="scr-table"><thead><tr>' +
      "<th>Number</th><th>Name</th><th>3.60 address</th><th>In 3.80</th><th>Size</th>" +
      '<th title="Heuristic: r4–r7 read before written">Args?</th><th>Calls</th><th>Hardware?</th>' +
      '</tr></thead><tbody id="scr-body"></tbody></table></div>' +
      '<p><button type="button" class="md-button" id="scr-more">Show more</button></p>';

    var q = app.querySelector("#scr-q"), body = app.querySelector("#scr-body");
    var count = app.querySelector("#scr-count"), more = app.querySelector("#scr-more");
    var note = app.querySelector("#scr-note");
    var f = {};
    ["named", "gint", "lib", "wp", "changed", "stub", "via"].forEach(function (k) {
      f[k] = app.querySelector("#scr-" + k);
    });
    var catSel = app.querySelector("#scr-cat");
    var open = {}, shown = PAGE, result = [];

    function addrMatches(a) {
      // an address in either OS: the syscall(s) whose function contains it
      a = ((a & 0x1FFFFFFF) | 0x80000000) >>> 0;
      var out = [], v60 = [], v80 = [];
      for (var i = 0; i < N; i++) {
        var r = rows[i], hit = false;
        if (a >= r[0] && a < r[0] + Math.max(r[3], 2)) { v60.push(i); hit = true; }
        if (r[1] && a >= r[1] && a < r[1] + Math.max(r[4], 2)) { v80.push(i); hit = true; }
        if (hit) out.push(i);
      }
      function say(list, os) {
        if (!list.length) return "no syscall's function contains it in " + os;
        return "in " + os + " it is inside " + list.map(function (i) {
          return num(i) + (rows[i][os === "3.60" ? 0 : 1] === a ? " (its entry)" : "");
        }).join(", ");
      }
      note.textContent = addr(a) + ": " + say(v60, "3.60") + "; " + say(v80, "3.80") + ".";
      return out;
    }

    function search() {
      var s = q.value.trim(), sl = s.toLowerCase(), hits = null, exact = {};
      note.textContent = "";
      if (s) {
        hits = [];
        var hexm = /^(0x)?([0-9a-f]+)h?$/i.exec(s);
        if (hexm) {
          var v = parseInt(hexm[2], 16);
          if (hexm[2].length >= 7 || v >= 0x80000000) addrMatches(v).forEach(function (i) { exact[i] = 1; });
          else {
            if (v < N) exact[v] = 1;
            if (!hexm[1] && /^[0-9]+$/.test(s) && +s < N) exact[+s] = 1;
          }
        }
        for (var i = 0; i < N; i++) {
          if (exact[i]) continue;
          var e = names[i];
          var hay = e ? [e.n, e.libfxcg, e.gint, e.ours, e.wp].join(" ").toLowerCase() : "";
          if ((hay && hay.indexOf(sl) >= 0) || (rows[i][11] && rows[i][11].toLowerCase().indexOf(sl) >= 0)) hits.push(i);
        }
        hits = Object.keys(exact).map(Number).sort(function (a, b) { return a - b; }).concat(hits);
      }
      var list = hits || order;
      var c = catSel.value === "" ? -1 : +catSel.value;
      result = list.filter(function (i) {
        var r = rows[i], e = names[i];
        if (f.named.checked && !(e && e.n)) return false;
        if (f.gint.checked && !(e && e.gint)) return false;
        if (f.lib.checked && !(e && e.libfxcg)) return false;
        if (f.wp.checked && !(e && e.wp)) return false;
        if (f.changed.checked && r[2] < 2) return false;
        if (f.stub.checked && !(r[5] & 19)) return false;
        if (c >= 0) {
          var m = r[9] | (f.via.checked ? r[10] : 0);
          if (!(m & (1 << c))) return false;
        }
        return true;
      });
      shown = PAGE;
      render();
      try {
        var h = s ? "#q=" + encodeURIComponent(s) : "";
        if (location.hash !== h) history.replaceState(null, "", h || location.pathname + location.search);
      } catch (err) { /* ignore */ }
    }

    function catChips(m, via) {
      var out = [];
      cats.forEach(function (c, k) {
        if (m & (1 << k)) out.push('<span class="scr-chip">' + esc(c[0]) + "</span>");
        else if (via & (1 << k)) out.push('<span class="scr-chip scr-via" title="through a call">' + esc(c[0]) + "</span>");
      });
      return out.join(" ");
    }
    function badges(i) {
      var e = names[i], b = [];
      if (!e) return "";
      if (e.libfxcg) b.push('<span class="scr-badge" title="libfxcg: ' + esc(e.libfxcg) + '">L</span>');
      if (e.gint) b.push('<span class="scr-badge" title="used by gint: ' + esc(e.gint) + '">G</span>');
      if (e.wp) b.push('<span class="scr-badge" title="WikiPrizm page">W</span>');
      if (e.ours || (e.doc && e.doc.name)) b.push('<span class="scr-badge scr-ours" title="named by this project">ours</span>');
      return b.join("");
    }
    function link(i) {
      return '<a href="#q=' + num(i) + '" data-n="' + i + '">' + num(i) + "</a>" +
        (nameOf(i) ? " " + esc(nameOf(i)) : "");
    }

    function rowHtml(i) {
      var r = rows[i], fl = r[5];
      var nm = nameOf(i) ? esc(nameOf(i)) : '<span class="scr-dim">—</span>';
      if (fl & 1) nm += ' <span class="scr-chip">empty</span>';
      if (fl & 2) nm += ' <span class="scr-chip">returns ' + r[12] + "</span>";
      if (fl & 16) nm += ' <span class="scr-chip">returns r4</span>';
      var cmp = CMP[r[2]];
      return '<tr class="scr-row' + (open[i] ? " scr-open" : "") + '" data-i="' + i + '" tabindex="0">' +
        '<td><code>' + num(i) + '</code><span class="scr-dec">' + i + "</span></td>" +
        "<td>" + nm + badges(i) + "</td>" +
        "<td><code>" + addr(r[0]) + "</code></td>" +
        '<td title="' + esc(cmp[1]) + '"><span class="scr-cmp scr-cmp' + r[2] + '">' + cmp[0] + "</span></td>" +
        "<td>" + r[3] + (fl & 4 ? "?" : "") + "</td>" +
        "<td>" + (fl & 3 ? (fl & 2 ? "—" : "none") : argText(r[6])) + "</td>" +
        "<td>" + (r[7].length || "") + "</td>" +
        "<td>" + catChips(r[9], r[10]) + "</td></tr>" +
        (open[i] ? '<tr class="scr-detail"><td colspan="8">' + detail(i) + "</td></tr>" : "");
    }

    function detail(i) {
      var r = rows[i], e = names[i] || {}, out = [];
      function item(k, v) { out.push("<dt>" + k + "</dt><dd>" + v + "</dd>"); }
      var names2 = [];
      if (e.libfxcg) names2.push("libfxcg: <code>" + esc(e.libfxcg) + "</code>");
      if (e.gint) names2.push('gint: <a href="' + GINT + '"><code>' + esc(e.gint) + "</code></a>");
      if (e.wp) names2.push('WikiPrizm: <a href="' + WP + esc(e.wp) + '">' + esc(e.wp.replace(/\/$/, "").split("/").pop()) + "</a>");
      if (e.ours) names2.push("ours: " + esc(e.ours));
      if (names2.length) item("Names", names2.join("<br>"));
      if (e.doc) {
        var dd = [];
        if (e.doc.prototype) dd.push("<code>" + esc(e.doc.prototype) + "</code>");
        if (e.doc.description) dd.push(esc(e.doc.description));
        if (e.doc.confidence) dd.push('<span class="scr-chip">' + esc(e.doc.confidence) + "</span>");
        if (dd.length) item("Our notes", dd.join("<br>"));
      }
      if (e.pages) item("On this site", e.pages.map(function (p) {
        return '<a href="' + root + p.replace(/\.md$/, "/") + '">' + esc(p.replace(/\.md$/, "")) + "</a>";
      }).join(", "));
      item("Addresses", "3.60 <code>" + addr(r[0]) + "</code>, " + r[3] + " bytes" + (r[5] & 4 ? " (end uncertain: an indirect jump)" : "") +
        "<br>3.80 <code>" + (r[1] ? addr(r[1]) : "—") + "</code>, " + r[4] + " bytes: " + esc(CMP[r[2]][1]));
      var al = byAddr[r[0]].filter(function (j) { return j !== i; });
      if (al.length) item("Same function as", al.map(link).join(", "));
      if (!(r[5] & 3)) item("Likely arguments (heuristic)", r[6] ? ["r4", "r5", "r6", "r7"].filter(function (x, b) { return r[6] & (1 << b); }).join(", ") +
        (r[6] === 15 ? " (perhaps more on the stack)" : "") : "none read");
      if (r[7].length) item("Calls (" + r[7].length + ")", r[7].map(function (c) {
        return c < 0x10000 ? link(c) : "<code>" + addr(c) + "</code>";
      }).join(", "));
      var cb = callers[i];
      if (cb && cb.length) item("Called by (" + cb.length + " syscalls)", cb.slice(0, 40).map(link).join(", ") + (cb.length > 40 ? ", …" : ""));
      if (r[8].length) item("I/O addresses (hardware guess)", r[8].map(function (v) {
        return "<code>" + addr(v) + "</code>";
      }).join(", ") + "<br>" + cats.filter(function (c, k) { return r[9] & (1 << k); }).map(function (c) {
        return c[2] ? '<a href="' + root + c[2] + '">' + esc(c[1]) + "</a>" : esc(c[1]);
      }).join(", "));
      if (r[10]) item("Hardware through calls (≤ 2 deep)", cats.filter(function (c, k) { return r[10] & (1 << k); }).map(function (c) {
        return esc(c[1]);
      }).join(", "));
      if (r[11]) item("Message it uses", "<code>" + esc(r[11]) + "</code>");
      var nb = neighbours(i);
      item("Named neighbours (3.60 address order)",
        (nb[0] !== null ? "before: " + link(nb[0]) + " <code>" + addr(rows[nb[0]][0]) + "</code>" : "before: —") + "<br>" +
        (nb[1] !== null ? "after: " + link(nb[1]) + " <code>" + addr(rows[nb[1]][0]) + "</code>" : "after: —"));
      return '<dl class="scr-dl">' + out.join("") + "</dl>";
    }

    function render() {
      var n = Math.min(shown, result.length), html = [];
      for (var k = 0; k < n; k++) html.push(rowHtml(result[k]));
      body.innerHTML = html.join("") ||
        '<tr><td colspan="8" class="scr-dim">No syscall matches.</td></tr>';
      count.textContent = result.length + " of " + N + " syscalls" +
        (result.length > n ? ", showing the first " + n : "");
      more.style.display = result.length > n ? "" : "none";
    }

    function goTo(n) {
      q.value = num(n);
      open = {};
      open[n] = 1;
      search();
      q.scrollIntoView({ block: "start" });
    }

    body.addEventListener("click", function (ev) {
      var a = ev.target.closest("a[data-n]");
      if (a) { ev.preventDefault(); goTo(+a.getAttribute("data-n")); return; }
      if (ev.target.closest("a")) return;
      var tr = ev.target.closest("tr.scr-row");
      if (!tr) return;
      var i = +tr.getAttribute("data-i");
      if (open[i]) delete open[i]; else open[i] = 1;
      render();
    });
    body.addEventListener("keydown", function (ev) {
      if (ev.key !== "Enter" && ev.key !== " ") return;
      var tr = ev.target.closest("tr.scr-row");
      if (!tr || ev.target !== tr) return;
      ev.preventDefault();
      var i = +tr.getAttribute("data-i");
      if (open[i]) delete open[i]; else open[i] = 1;
      render();
      var again = body.querySelector('tr.scr-row[data-i="' + i + '"]');
      if (again) again.focus();
    });
    more.addEventListener("click", function () { shown += PAGE * 5; render(); });
    var timer = null;
    q.addEventListener("input", function () { clearTimeout(timer); timer = setTimeout(search, 120); });
    Object.keys(f).forEach(function (k) { f[k].addEventListener("change", search); });
    catSel.addEventListener("change", search);

    var m = /[#&]q=([^&]*)/.exec(location.hash);
    if (m) {
      q.value = decodeURIComponent(m[1]);
      var hm = /^0x([0-9a-f]{1,4})$/i.exec(q.value);
      if (hm && parseInt(hm[1], 16) < N) open[parseInt(hm[1], 16)] = 1;
    }
    search();
  }
})();
