(function () {
  "use strict";

  // Accepts either {id: n} or [{id, count}] for favourites, and {id: {love, ok, no}} or
  // [{id, love, ok, no}] for reactions. Comments may be strings or {comment, email?, created_at?}.
  function $(id) { return document.getElementById(id); }
  function h(tag, props) {
    var el = document.createElement(tag);
    var p = props || {};
    Object.keys(p).forEach(function (k) {
      if (k === "text") el.textContent = p[k];
      else if (k === "class") el.className = p[k];
      else if (p[k] != null && p[k] !== false) el.setAttribute(k, p[k]);
    });
    for (var i = 2; i < arguments.length; i++) if (arguments[i]) el.appendChild(arguments[i]);
    return el;
  }

  function byId(v, field) {
    var out = {};
    if (Array.isArray(v)) v.forEach(function (x) { out[x.id || x.option || x.favourite] = x; });
    else if (v && typeof v === "object") Object.keys(v).forEach(function (k) { out[k] = v[k]; });
    return out;
  }
  function count(x) {
    if (typeof x === "number") return x;
    return x && typeof x === "object" ? Number(x.count || x.votes || x.n || 0) : 0;
  }

  function render(options, data) {
    var fav = byId(data.favourites || data.favorites || data.counts);
    var rx = byId(data.reactions);
    var comments = data.comments || [];
    var total = 0;
    var ids = options.map(function (o) { return o.id; });
    Object.keys(fav).forEach(function (k) { if (ids.indexOf(k) < 0) ids.push(k); });
    Object.keys(fav).forEach(function (k) { total += count(fav[k]); });
    if (typeof data.total === "number") total = data.total;

    var names = {}; var icons = {};
    options.forEach(function (o) { names[o.id] = o.name; icons[o.id] = o.images && o.images.icon; });
    ids.sort(function (a, b) { return count(fav[b]) - count(fav[a]); });

    $("summary").textContent = total + (total === 1 ? " vote" : " votes") + " so far, " + comments.length + (comments.length === 1 ? " comment." : " comments.");
    var rows = $("rows");
    rows.textContent = "";
    ids.forEach(function (id) {
      var n = count(fav[id]);
      var r = rx[id] || {};
      var who = h("div", { class: "who-cell" });
      if (icons[id]) who.appendChild(h("img", { src: "/" + icons[id], alt: "", width: 40, height: 40 }));
      who.appendChild(h("span", { text: names[id] || id }));
      var bar = h("progress", { max: Math.max(total, 1), value: n, "aria-label": (names[id] || id) + " share of favourites" });
      rows.appendChild(h("tr", {},
        h("th", { scope: "row" }, who),
        h("td", { class: "num", text: String(n) }),
        h("td", {}, bar),
        h("td", { class: "num", text: String(count(r.love)) }),
        h("td", { class: "num", text: String(count(r.ok)) }),
        h("td", { class: "num", text: String(count(r.no)) })));
    });

    var list = $("comments");
    list.textContent = "";
    $("comments-hint").textContent = comments.length ? "" : "No comments yet.";
    comments.forEach(function (c) {
      var text = typeof c === "string" ? c : c.comment || c.text || "";
      var meta = typeof c === "object" ? [c.email, c.favourite && "favourite: " + (names[c.favourite] || c.favourite), c.created_at || c.at].filter(Boolean).join(", ") : "";
      var li = h("li", { text: text });
      if (meta) li.appendChild(h("span", { class: "meta", text: meta }));
      list.appendChild(li);
    });
    $("out").hidden = false;
  }

  function get(url) {
    return fetch(url, { headers: { Accept: "application/json" }, credentials: "same-origin" }).then(function (r) {
      if (!r.ok) { var e = new Error("status"); e.status = r.status; throw e; }
      return r.json();
    });
  }

  Promise.all([get("/api/results"), get("/api/options").catch(function () { return []; })])
    .then(function (v) { render(Array.isArray(v[1]) ? v[1] : [], v[0]); })
    .catch(function (e) {
      $("summary").textContent = "";
      $(e.status === 401 || e.status === 403 ? "denied" : "failed").hidden = false;
    });
})();
