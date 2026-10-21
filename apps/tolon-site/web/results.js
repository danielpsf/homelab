// Builds the results markup from results.json and options. Runs in two places: tools/build-results.mjs
// pre-renders the English page into index.html (so curl and crawlers see the results), and the browser
// calls it again in the chosen language. Plain script, no inline code (CSP).
(function (root) {
  "use strict";

  var GITHUB = "https://github.com/danielpsf/tolon";
  var REACTIONS = [["love", "results.thLove"], ["ok", "results.thOk"], ["no", "results.thNo"]];

  function esc(s) {
    return String(s).replace(/[&<>"']/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]; });
  }
  function asset(p) { return "/" + String(p).replace(/^\/+/, ""); }
  function srcset(small) { return asset(small) + " 1x, " + asset(small.replace(/16\.png$/, "32.png")) + " 2x"; }
  function n(x) { return typeof x === "number" ? x : 0; }

  // Most favorites first; a tie goes to the option with more "love it" reactions, then to the file order.
  function rank(options, data) {
    var love = function (o) { return n(((data.reactions || {})[o.id] || {}).love); };
    var fav = function (o) { return n((data.favourites || {})[o.id]); };
    return options.map(function (o, i) { return { o: o, i: i }; }).sort(function (a, b) {
      return fav(b.o) - fav(a.o) || love(b.o) - love(a.o) || a.i - b.i;
    }).map(function (x) { return x.o; });
  }

  function bar(value, max) {
    return '<progress max="' + Math.max(max, 1) + '" value="' + value + '" aria-hidden="true"></progress>';
  }

  // Numbers are always text next to the bar, so no meaning rests on the bar or its colour.
  function stats(o, data, T) {
    var fav = n((data.favourites || {})[o.id]);
    var rx = (data.reactions || {})[o.id] || {};
    var rows = REACTIONS.map(function (r) {
      var v = n(rx[r[0]]);
      return '<li><span class="stat-label">' + esc(T(r[1])) + '</span><strong class="stat-n">' + v + "</strong>" + bar(v, data.total) + "</li>";
    }).join("");
    return '<div class="stats"><p class="stat"><span class="stat-label">' + esc(T("final.favorites")) + '</span><strong class="stat-n">' + fav + "</strong>" +
      bar(fav, data.total) + '</p><div class="stat"><p class="stat-title">' + esc(T("final.reactions")) + '</p><ul class="stat-list">' + rows + "</ul></div></div>";
  }

  function head(o, T, level) {
    var h = "h" + level;
    return '<div class="card-head"><img class="card-icon" src="' + esc(asset(o.images.icon)) + '" width="112" height="112" alt="' + esc(T("card.iconAlt", { name: o.name })) + '">' +
      "<div><" + h + ' class="card-name">' + esc(o.name) + "</" + h + '><p class="tagline">' + esc(o.tagline) + '</p><p class="menubar"><span class="bar"><img src="' +
      esc(asset(o.images.small)) + '" srcset="' + esc(srcset(o.images.small)) + '" width="16" height="16" alt="' + esc(T("card.smallAlt", { name: o.name })) + '"></span><span>' +
      esc(T("card.menubar")) + "</span></p></div></div>";
  }

  function render(data, options, T) {
    var list = rank(options, data);
    var w = list[0];
    var wins = n((data.favourites || {})[w.id]);
    var out = '<header class="hero final"><p class="eyebrow">' + esc(T("final.closed")) + "</p><h1>" + esc(T("final.h1", { name: w.name })) + "</h1><p>" +
      esc(T("final.lead", { name: w.name, n: wins, total: data.total })) + '</p><p class="total"><span>' + esc(T("final.totalVoters")) + "</span> <strong>" + data.total + "</strong></p></header>";

    out += '<section class="winner" id="winner" aria-label="' + esc(w.name) + '"><div class="card winner-card"><div class="winner-main">' + head(w, T, 2) + stats(w, data, T) + "</div>" +
      '<figure class="scene"><img src="' + esc(asset(w.images.scene)) + '" alt="' + esc(T("pv.sceneAlt", { name: w.name })) + '" loading="lazy"><figcaption>' +
      esc(T("pv.sceneTitle")) + ". " + esc(T("pv.sceneCaption", { name: w.name, tagline: w.tagline })) + "</figcaption></figure></div></section>";

    out += '<section aria-labelledby="others"><h2 class="section-title" id="others">' + esc(T("final.othersTitle")) + '</h2><p class="section-hint">' + esc(T("final.othersHint")) + '</p><ul class="cards">' +
      list.slice(1).map(function (o) { return '<li class="card">' + head(o, T, 3) + stats(o, data, T) + "</li>"; }).join("") + "</ul></section>";

    out += '<section class="note ok" aria-labelledby="next"><h2 id="next">' + esc(T("final.nextTitle")) + "</h2><p>" + esc(T("final.nextText", { name: w.name })) +
      '</p><p><a class="sm-btn primary" href="' + GITHUB + '" target="_blank" rel="noopener">' + esc(T("common.github")) + '</a></p></section><p class="note">' + esc(T("final.privacy")) + "</p>";
    return out;
  }

  if (typeof module !== "undefined" && module.exports) module.exports = { render: render };
  else root.renderResults = render;
})(typeof window !== "undefined" ? window : this);
