(function () {
  "use strict";

  var params = new URLSearchParams(window.location.search);
  // Language the real Tolon UI is shown in, by page language. The app has these catalogues today;
  // every other page language shows the app in en-US. Add a row when the app ships a language.
  var UI_LANG = { "pt-BR": "pt-BR", "en-US": "en-US" };
  var options = [];
  var T = function (k, p) { return window.i18n.t(k, p); };

  function $(id) { return document.getElementById(id); }
  function asset(p) { return "/" + String(p).replace(/^\/+/, ""); }
  function srcset(small) { return asset(small) + " 1x, " + asset(small.replace(/16\.png$/, "32.png")) + " 2x"; }

  function getJSON(url) {
    return fetch(url, { headers: { Accept: "application/json" }, credentials: "same-origin" }).then(function (r) {
      if (!r.ok) throw new Error(url);
      return r.json();
    });
  }

  function choose(id) {
    var o = options.filter(function (x) { return x.id === id; })[0];
    if (!o) return;
    // The real Tolon UI reads ?lang= (ui/src/copy.ts); sl is the page language, used by the way back.
    var q = "?icon=" + encodeURIComponent(o.id) + "&lang=" + encodeURIComponent(UI_LANG[window.i18n.locale] || "en-US") + "&sl=" + encodeURIComponent(window.i18n.locale);
    $("show").href = "/preview/interrupt.html" + q;
    $("cal-link").href = "/preview/app.html" + q + "#/calendars";
    $("tray").src = asset(o.images.small);
    $("tray").srcset = srcset(o.images.small);
    $("dock").src = asset(o.images.icon);
    $("scene").src = asset(o.images.scene);
    $("scene").alt = T("pv.sceneAlt", { name: o.name });
    $("scene-cap").textContent = T("pv.sceneCaption", { name: o.name, tagline: o.tagline });
    $("desk-cap").textContent = T("pv.deskCaption", { name: o.name });
  }

  function buildChooser(first) {
    var box = $("chooser");
    options.forEach(function (o) {
      var id = "icon-" + o.id;
      var input = document.createElement("input");
      input.type = "radio"; input.name = "icon"; input.id = id; input.value = o.id; input.className = "pick";
      input.checked = o.id === first;
      input.addEventListener("change", function () { choose(o.id); });
      var label = document.createElement("label");
      label.htmlFor = id;
      var img = document.createElement("img");
      img.src = asset(o.images.icon); img.alt = ""; img.width = 56; img.height = 56;
      var name = document.createElement("span");
      name.textContent = o.name;
      label.appendChild(img); label.appendChild(name);
      var cell = document.createElement("div");
      cell.appendChild(input); cell.appendChild(label);
      box.appendChild(cell);
    });
  }

  window.i18n.ready.then(function () {
    if (!UI_LANG[window.i18n.locale]) {
      var l = window.i18n.locales.filter(function (x) { return x.code === window.i18n.locale; })[0];
      $("app-note").textContent = T("pv.appNote", { language: l.name });
      $("app-note").hidden = false;
    }
    var done = params.get("done");
    if (done && /^[a-z_]+$/.test(done) && T("pv.msg." + done) !== "pv.msg." + done) {
      var p = document.createElement("p");
      p.textContent = T("pv.msg." + done);
      $("status").appendChild(p);
    }
    return getJSON("/options.json").catch(function () { return getJSON("/api/options"); }).then(window.i18n.localizeOptions);
  }).then(function (list) {
    options = list;
    var want = params.get("icon");
    var first = options.some(function (o) { return o.id === want; }) ? want : options[0].id;
    buildChooser(first);
    choose(first);
  }).catch(function () { $("load-error").hidden = false; });
})();
