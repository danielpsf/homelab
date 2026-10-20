// Language support for every page: picks the locale, loads /i18n/<locale>.json, fills data-i18n elements
// and adds the flag switcher to the .top bar. Plain script, no inline code (CSP).
(function () {
  "use strict";

  // Menu order as agreed with Daniel; the first match of the browser language decides.
  var LOCALES = [
    { code: "pt-BR", short: "PT-BR", flag: "br", name: "Português (Brasil)", dir: "ltr" },
    { code: "de", short: "DE", flag: "de", name: "Deutsch", dir: "ltr" },
    { code: "en-US", short: "EN-US", flag: "us", name: "English (US)", dir: "ltr" },
    { code: "es", short: "ES", flag: "es", name: "Español", dir: "ltr" },
    { code: "ar", short: "AR", flag: "sa", name: "العربية", dir: "rtl" },
    { code: "tr", short: "TR", flag: "tr", name: "Türkçe", dir: "ltr" },
    { code: "hi", short: "HI", flag: "in", name: "हिन्दी", dir: "ltr" },
    { code: "zh-CN", short: "ZH-CN", flag: "cn", name: "简体中文", dir: "ltr" },
    { code: "ja", short: "JA", flag: "jp", name: "日本語", dir: "ltr" },
    { code: "he", short: "HE", flag: "il", name: "עברית", dir: "rtl" },
  ];
  var FALLBACK = "en-US";
  var STORE = "tolon.lang";
  // Any pt-* is Brazilian Portuguese, any en-* is American English, any es-*, zh-* and so on map to the one shipped variant.
  var BY_LANGUAGE = { pt: "pt-BR", en: "en-US", de: "de", es: "es", ar: "ar", tr: "tr", hi: "hi", zh: "zh-CN", ja: "ja", he: "he", iw: "he" };


  var root = document.documentElement;
  root.classList.add("i18n-pending");
  var pendingTimer = setTimeout(function () { root.classList.remove("i18n-pending"); }, 3000);

  function matchTag(tag) {
    var loc;
    try { loc = new Intl.Locale(String(tag).replace(/_/g, "-")); } catch (e) { return null; }
    return BY_LANGUAGE[loc.language] || null;
  }

  function known(code) {
    return LOCALES.some(function (l) { return l.code === code; }) ? code : null;
  }

  function saved() {
    try { return known(window.localStorage.getItem(STORE)); } catch (e) { return null; }
  }

  // 1. ?lang=  2. saved choice  3. navigator.languages  4. en-US
  function resolve() {
    var q = new URLSearchParams(window.location.search).get("lang");
    var fromQuery = q ? matchTag(q) : null;
    if (fromQuery) return fromQuery;
    var s = saved();
    if (s) return s;
    var langs = navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language];
    for (var i = 0; i < langs.length; i++) {
      var m = langs[i] ? matchTag(langs[i]) : null;
      if (m) return m;
    }
    return FALLBACK;
  }

  var locale = resolve();
  root.lang = locale;
  root.dir = LOCALES.filter(function (l) { return l.code === locale; })[0].dir;
  var dict = {};
  var hooks = [];

  // Plural keys: <key>.<zero|one|two|few|many|other>, chosen with Intl.PluralRules; .other is the fallback.
  function raw(key, params) {
    var k = key;
    if (params && typeof params.n === "number" && dict[key + ".other"] != null) {
      var cat = new Intl.PluralRules(locale).select(params.n);
      k = dict[key + "." + cat] != null ? key + "." + cat : key + ".other";
    }
    return dict[k] != null ? dict[k] : key;
  }

  function t(key, params) {
    return raw(key, params).replace(/\{(\w+)\}/g, function (m, name) {
      return params && params[name] != null ? String(params[name]) : m;
    });
  }

  // Like t(), but a parameter may be a DOM node (a link, a bold name).
  function node(key, params) {
    var frag = document.createDocumentFragment();
    raw(key, params).split(/(\{\w+\})/).forEach(function (part) {
      var m = /^\{(\w+)\}$/.exec(part);
      var v = m && params && params[m[1]] != null ? params[m[1]] : part;
      if (v === "") return;
      frag.appendChild(typeof v === "object" ? v : document.createTextNode(String(v)));
    });
    return frag;
  }

  function apply(scope) {
    var r = scope || document;
    r.querySelectorAll("[data-i18n]").forEach(function (el) {
      var k = el.getAttribute("data-i18n");
      if (dict[k] != null) el.textContent = dict[k];
    });
    r.querySelectorAll("[data-i18n-attr]").forEach(function (el) {
      el.getAttribute("data-i18n-attr").split(";").forEach(function (pair) {
        var p = pair.split("=");
        if (p.length === 2 && dict[p[1].trim()] != null) el.setAttribute(p[0].trim(), dict[p[1].trim()]);
      });
    });
  }

  function getJSON(url) {
    return fetch(url, { headers: { Accept: "application/json" } }).then(function (r) {
      if (!r.ok) throw new Error(url);
      return r.json();
    });
  }

  // Option texts live in /options.<locale>.json, keyed by option id. The ids and images come from the API.
  function localizeOptions(list) {
    return getJSON("/options." + locale + ".json").catch(function () { return {}; }).then(function (map) {
      return list.map(function (o) {
        var x = map[o.id];
        return x ? Object.assign({}, o, { name: x.name, tagline: x.tagline, story: x.story, history: x.history }) : o;
      });
    });
  }

  function formatDate(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) return "";
    return new Intl.DateTimeFormat(locale, { dateStyle: "long" }).format(d);
  }

  // ---- switcher ----
  function choose(code) {
    if (code !== locale) {
      try { window.localStorage.setItem(STORE, code); } catch (e) {}
      hooks.forEach(function (h) { try { h(); } catch (e) {} });
      var u = new URL(window.location.href);
      u.searchParams.delete("lang");
      if (u.href === window.location.href) window.location.reload();
      else window.location.assign(u.href);
    }
  }

  function flagImg(l) {
    var img = document.createElement("img");
    img.src = "/flags/" + l.flag + ".svg";
    img.alt = "";
    img.width = 20;
    img.height = 15;
    img.className = "flag sm-menu-icon";
    return img;
  }

  // Swiss Moss menu button (class sm-menu, data-sm-menu); swiss-moss.js adds the keyboard behaviour.
  function buildSwitcher() {
    var top = document.querySelector(".top");
    if (!top) return;
    var cur = LOCALES.filter(function (l) { return l.code === locale; })[0];
    var wrap = document.createElement("div");
    wrap.className = "lang sm-menu end";
    wrap.setAttribute("data-sm-menu", "");
    var btn = document.createElement("button");
    btn.type = "button";
    btn.className = "sm-btn sm lang-btn";
    btn.id = "lang-btn";
    btn.setAttribute("aria-haspopup", "menu");
    btn.setAttribute("aria-expanded", "false");
    btn.setAttribute("aria-controls", "lang-menu");
    btn.setAttribute("aria-label", t("lang.label") + ": " + cur.name);
    btn.appendChild(flagImg(cur));
    var code = document.createElement("span");
    code.textContent = cur.short;
    btn.appendChild(code);
    var menu = document.createElement("ul");
    menu.className = "sm-menu-list";
    menu.id = "lang-menu";
    menu.setAttribute("role", "menu");
    menu.setAttribute("aria-label", t("lang.label"));
    menu.hidden = true;
    LOCALES.forEach(function (l) {
      var li = document.createElement("li");
      li.setAttribute("role", "none");
      var b = document.createElement("button");
      b.type = "button";
      b.setAttribute("role", "menuitemradio");
      b.setAttribute("aria-checked", String(l.code === locale));
      b.setAttribute("lang", l.code);
      b.setAttribute("data-value", l.code);
      b.appendChild(flagImg(l));
      var s = document.createElement("span");
      s.textContent = l.name;
      b.appendChild(s);
      li.appendChild(b);
      menu.appendChild(li);
    });
    wrap.appendChild(btn);
    wrap.appendChild(menu);
    wrap.addEventListener("sm:select", function (e) { choose(e.detail.value); });
    top.appendChild(wrap);
    if (window.SwissMoss) window.SwissMoss.menu.init(wrap);
  }

  // Links that lead home keep an explicit ?lang= when the page was opened with one.
  function homeLinks() {
    var q = new URLSearchParams(window.location.search).get("lang");
    document.querySelectorAll("a[data-home]").forEach(function (a) {
      a.setAttribute("href", q ? "/?lang=" + encodeURIComponent(q) : "/");
    });
  }

  function finish() {
    root.lang = locale;
    root.dir = LOCALES.filter(function (l) { return l.code === locale; })[0].dir;
    apply();
    homeLinks();
    // swiss-moss.js is deferred and runs before DOMContentLoaded; build the menu once it is there (or after load).
    var built = false;
    var build = function () { if (!built) { built = true; buildSwitcher(); } };
    if (window.SwissMoss || document.readyState === "complete") build();
    else { document.addEventListener("DOMContentLoaded", build); window.addEventListener("load", build); }
    clearTimeout(pendingTimer);
    root.classList.remove("i18n-pending");
  }

  var ready = Promise.all([getJSON("/i18n/" + FALLBACK + ".json"), locale === FALLBACK ? null : getJSON("/i18n/" + locale + ".json")])
    .then(function (v) { dict = Object.assign({}, v[0], v[1] || {}); })
    .catch(function () { /* keep the English text that is in the HTML */ })
    .then(finish);

  window.i18n = {
    locale: locale,
    locales: LOCALES,
    ready: ready,
    t: t,
    node: node,
    apply: apply,
    formatDate: formatDate,
    localizeOptions: localizeOptions,
    beforeChange: function (fn) { hooks.push(fn); },
  };
})();
