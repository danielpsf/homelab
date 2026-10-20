// Stands in for the Tauri runtime so the real Tolon UI runs in a plain browser with sample data.
// Loaded before the app bundle by app.html and interrupt.html. Nothing here leaves the page.
(function () {
  "use strict";
  var MIN = 60000;
  var qs = new URLSearchParams(window.location.search);
  // Sample texts follow the language the UI is shown in (?lang=); the way back follows the page language (?sl=).
  var SAMPLE = {
    "pt-BR": { c1: "Trabalho", c2: "Time de plataforma", meeting: "Reunião semanal da plataforma" },
    "en-US": { c1: "Work", c2: "Platform team", meeting: "Platform weekly sync" },
  };
  var BACK = {
    "pt-BR": "Voltar para a prévia dos ícones", de: "Zurück zur Icon-Vorschau", "en-US": "Back to the icon preview",
    es: "Volver a la vista previa de iconos", ar: "العودة إلى معاينة الأيقونات", tr: "Simge önizlemesine dön",
    hi: "आइकन पूर्वावलोकन पर वापस जाएँ", "zh-CN": "返回图标预览", ja: "アイコンのプレビューに戻る",
  };
  var TEXT = SAMPLE[qs.get("lang")] || SAMPLE["en-US"];
  var BACK_TEXT = BACK[qs.get("sl")] || BACK["en-US"];
  var t0 = Date.now();
  var callbacks = {};
  var listeners = {};
  var nextId = 1;
  var nextCal = 3;

  function iso(ms) {
    return new Date(ms).toISOString();
  }

  var calendars = [
    { id: "cal-1", name: TEXT.c1, kind: "ics", enabled: true, display_url: "https://calendar.example.com", last_sync_at: iso(t0 - 2 * MIN), last_error: null },
    { id: "cal-2", name: TEXT.c2, kind: "ics", enabled: true, display_url: "https://teams.example.org", last_sync_at: iso(t0 - 7 * MIN), last_error: null },
  ];

  // The next whole minute, at least 30 s away, so the countdown reads "Starts in 1 min".
  var start = Math.ceil((t0 + 30000) / MIN) * MIN;
  var alarm = {
    test: false,
    meetings: [
      {
        key: "sample-1",
        title: TEXT.meeting,
        start: iso(start),
        end: iso(start + 45 * MIN),
        join: { kind: "link", host: "us02web.zoom.us", recognised: true },
        people: 8,
      },
    ],
  };

  function emit(event, payload) {
    (listeners[event] || []).slice().forEach(function (id) {
      if (callbacks[id]) callbacks[id]({ event: event, id: id, payload: payload });
    });
  }

  function reject(code) {
    return Promise.reject({ code: code });
  }

  var icon = qs.get("icon");
  var sl = qs.get("sl");
  // Back to the shell: its own ?lang= is the page language (sl), not the UI language.
  var extra = (icon ? "&icon=" + encodeURIComponent(icon) : "") + (sl ? "&lang=" + encodeURIComponent(sl) : "");
  var back = "/preview/?" + extra.slice(1);

  // The real app closes its window; here the page returns to the preview and says what the press would do.
  function say(action) {
    window.location.href = "/preview/?done=" + action + extra;
    return Promise.resolve();
  }

  if (/app\.html$/.test(window.location.pathname)) {
    document.addEventListener("DOMContentLoaded", function () {
      var a = document.createElement("a");
      a.className = "sim-back";
      a.href = back;
      a.textContent = BACK_TEXT;
      document.body.appendChild(a);
    });
  }

  function addIcs(url) {
    var u;
    try {
      u = new URL(String(url));
    } catch (e) {
      return reject("invalid_url");
    }
    if (u.protocol !== "https:" && u.protocol !== "http:" && u.protocol !== "webcal:") return reject("unsupported_scheme");
    var cal = { id: "cal-" + nextCal++, name: u.hostname, kind: "ics", enabled: true, display_url: u.protocol + "//" + u.hostname, last_sync_at: iso(Date.now()), last_error: null };
    calendars.push(cal);
    setTimeout(function () { emit("calendars:changed", null); }, 0);
    return Promise.resolve(cal);
  }

  var commands = {
    calendars_list: function () { return Promise.resolve(calendars.map(function (c) { return Object.assign({}, c); })); },
    calendars_add_ics: function (a) { return addIcs(a.url); },
    calendars_remove: function (a) {
      calendars = calendars.filter(function (c) { return c.id !== a.id; });
      setTimeout(function () { emit("calendars:changed", null); }, 0);
      return Promise.resolve(null);
    },
    calendars_set_enabled: function (a) {
      calendars.forEach(function (c) { if (c.id === a.id) c.enabled = a.enabled; });
      setTimeout(function () { emit("calendars:changed", null); }, 0);
      return Promise.resolve(null);
    },
    alarms_current: function () { return Promise.resolve(alarm); },
    alarms_snooze: function () { return say("snooze"); },
    alarms_dismiss: function () { return say("dismiss"); },
    alarms_join: function () { return say("join"); },
    alarms_already_there: function () { return say("already_there"); },
  };

  window.__TAURI_EVENT_PLUGIN_INTERNALS__ = {
    unregisterListener: function (event, id) {
      delete callbacks[id];
      listeners[event] = (listeners[event] || []).filter(function (x) { return x !== id; });
    },
  };

  window.__TAURI_INTERNALS__ = {
    callbacks: callbacks,
    transformCallback: function (cb, once) {
      var id = nextId++;
      callbacks[id] = function (data) {
        if (once) delete callbacks[id];
        return cb && cb(data);
      };
      return id;
    },
    unregisterCallback: function (id) { delete callbacks[id]; },
    runCallback: function (id, data) { if (callbacks[id]) callbacks[id](data); },
    convertFileSrc: function (p) { return p; },
    invoke: function (cmd, args) {
      if (cmd === "plugin:event|listen") {
        (listeners[args.event] = listeners[args.event] || []).push(args.handler);
        return Promise.resolve(args.handler);
      }
      if (cmd === "plugin:event|unlisten") return Promise.resolve(null);
      var fn = commands[cmd];
      return fn ? fn(args || {}) : reject("internal");
    },
  };
})();
