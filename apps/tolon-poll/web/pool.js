(function () {
  "use strict";

  var MAX = 1000;
  var REACTIONS = ["love", "ok", "no"];
  var DRAFT = "tolon.draft";
  var options = [];
  var config = {};
  var T = function (k, p) { return window.i18n.t(k, p); };

  function $(id) { return document.getElementById(id); }

  function h(tag, props) {
    var el = document.createElement(tag);
    var p = props || {};
    Object.keys(p).forEach(function (k) {
      if (k === "text") el.textContent = p[k];
      else if (k === "class") el.className = p[k];
      else if (p[k] === true) el.setAttribute(k, "");
      else if (p[k] !== false && p[k] != null) el.setAttribute(k, p[k]);
    });
    for (var i = 2; i < arguments.length; i++) if (arguments[i]) el.appendChild(arguments[i]);
    return el;
  }

  function asset(path) { return "/" + String(path).replace(/^\/+/, ""); }
  // 32 px glyph as the 2x source; same folder, 32.png next to 16.png.
  function srcset(small) { return asset(small) + " 1x, " + asset(small.replace(/16\.png$/, "32.png")) + " 2x"; }

  function show(id) {
    ["loading", "load-error", "signin", "voted", "thanks", "poll"].forEach(function (x) { $(x).hidden = x !== id; });
  }

  function getJSON(url) {
    return fetch(url, { headers: { Accept: "application/json" }, credentials: "same-origin" }).then(function (r) {
      if (!r.ok) throw new Error(url + " " + r.status);
      return r.json();
    });
  }

  function post(url, body) {
    return fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify(body || {}),
    });
  }

  function radio(name, id, value, label, cls) {
    var wrap = h("div", { class: cls });
    wrap.appendChild(h("input", { class: "pick", type: "radio", name: name, id: id, value: value }));
    wrap.appendChild(h("label", { for: id, text: label }));
    return wrap;
  }

  // ---- privacy note, from /api/config ----
  function renderPrivacy() {
    var box = $("privacy");
    box.textContent = "";
    var list = h("ul");
    var add = function (child) { list.appendChild(h("li", {}, child)); };
    var text = function (key, params) { var s = h("span"); s.appendChild(window.i18n.node(key, params)); return s; };

    add(text("privacy.who"));
    add(text("privacy.what"));
    add(text("privacy.why"));

    var procs = Array.isArray(config.processors) && config.processors.length ? config.processors : ["cloudflare", "resend"];
    var sub = h("ul");
    procs.forEach(function (p) {
      var name = String(typeof p === "string" ? p : (p && (p.name || p.id)) || "").toLowerCase();
      var key = "privacy.processor." + name;
      sub.appendChild(h("li", { text: window.i18n.t(key) !== key ? T(key) : (typeof p === "string" ? p : (p.name || name)) }));
    });
    var pli = h("li", {}, text("privacy.processors"), sub);
    list.appendChild(pli);

    add(text("privacy.tracking"));

    var date = config.delete_after ? window.i18n.formatDate(config.delete_after) : "";
    add(date ? text("privacy.deletion", { date: h("strong", { text: date }) }) : text("privacy.deletionNoDate"));

    if (config.contact_email) {
      var a = h("a", { href: "mailto:" + config.contact_email, text: config.contact_email });
      add(text("privacy.ask", { contact: a }));
    } else {
      add(text("privacy.askNoContact"));
    }

    box.appendChild(h("h2", { id: "privacy-title", class: "privacy-title", text: T("privacy.title") }));
    box.appendChild(list);
    box.hidden = false;
  }

  // ---- cards ----
  function card(o) {
    var more = function (title, body) {
      var d = h("details", { class: "more" }, h("summary", { text: title }));
      d.appendChild(body);
      return d;
    };
    var steps = h("ol");
    (o.history || []).forEach(function (s) { steps.appendChild(h("li", { text: s })); });

    var react = h("fieldset", { class: "react" }, h("legend", { text: T("form.reactionLegend", { name: o.name }) }));
    var opts = h("div", { class: "opts" });
    REACTIONS.forEach(function (r) {
      opts.appendChild(radio("react-" + o.id, "react-" + o.id + "-" + r, r, T("form." + r), "opt"));
    });
    react.appendChild(opts);

    return h("li", { class: "card", "data-id": o.id },
      h("div", { class: "card-head" },
        h("img", { class: "card-icon", src: asset(o.images.icon), width: "112", height: "112", alt: T("card.iconAlt", { name: o.name }) }),
        h("div", {},
          h("h3", { class: "card-name", text: o.name }),
          h("p", { class: "tagline", text: o.tagline }),
          h("p", { class: "menubar" },
            h("span", { class: "bar" }, h("img", { src: asset(o.images.small), srcset: srcset(o.images.small), width: "16", height: "16", alt: T("card.smallAlt", { name: o.name }) })),
            h("span", { text: T("card.menubar") })))),
      more(T("form.storySummary"), h("p", { text: o.story })),
      more(T("form.historySummary"), steps),
      h("figure", { class: "scene" },
        h("img", { src: asset(o.images.scene), width: "1280", height: "800", loading: "lazy", alt: T("form.sceneAlt", { name: o.name }) }),
        h("figcaption", { text: T("form.sceneCaption") })),
      h("div", { class: "choices" },
        radio("favourite", "fav-" + o.id, o.id, T("form.favorite", { name: o.name }), "fav"),
        react));
  }

  function render() {
    var list = $("cards");
    list.textContent = "";
    options.forEach(function (o) { list.appendChild(card(o)); });
  }

  function nameOf(id) {
    for (var i = 0; i < options.length; i++) if (options[i].id === id) return options[i].name;
    return id;
  }

  // ---- draft kept across a language switch ----
  function saveDraft() {
    try {
      if ($("poll").hidden) return;
      var fav = document.querySelector('input[name="favourite"]:checked');
      var reactions = {};
      options.forEach(function (o) {
        var r = document.querySelector('input[name="react-' + o.id + '"]:checked');
        if (r) reactions[o.id] = r.value;
      });
      window.sessionStorage.setItem(DRAFT, JSON.stringify({ fav: fav ? fav.value : null, reactions: reactions, comment: $("comment").value }));
    } catch (e) {}
  }

  function restoreDraft() {
    try {
      var d = JSON.parse(window.sessionStorage.getItem(DRAFT) || "null");
      window.sessionStorage.removeItem(DRAFT);
      if (!d) return;
      if (d.fav && $("fav-" + d.fav)) $("fav-" + d.fav).checked = true;
      Object.keys(d.reactions || {}).forEach(function (id) {
        var el = $("react-" + id + "-" + d.reactions[id]);
        if (el) el.checked = true;
      });
      if (d.comment) $("comment").value = d.comment;
    } catch (e) {}
  }

  function boxMsg(id, msg) {
    var box = $(id);
    box.textContent = "";
    if (msg) box.appendChild(h("p", { text: msg }));
  }

  function updateCounter() {
    var n = $("comment").value.length;
    var c = $("counter");
    c.textContent = T("form.counter", { n: n, max: MAX });
    c.className = "counter" + (MAX - n <= 50 ? " low" : "");
  }

  function alreadyVoted() {
    show("voted");
    $("voted").focus();
  }

  function submit(ev) {
    ev.preventDefault();
    boxMsg("errors", "");
    var fav = document.querySelector('input[name="favourite"]:checked');
    if (!fav) {
      boxMsg("errors", T("form.err.pickFirst"));
      var first = document.querySelector('input[name="favourite"]');
      if (first) first.focus();
      return;
    }
    var reactions = {};
    options.forEach(function (o) {
      var r = document.querySelector('input[name="react-' + o.id + '"]:checked');
      if (r) reactions[o.id] = r.value;
    });
    var body = { favourite: fav.value, reactions: reactions, comment: $("comment").value.trim() };
    var btn = $("send");
    var retry = function (key) {
      btn.disabled = false;
      btn.textContent = T("form.submit");
      boxMsg("errors", T(key));
      btn.focus();
    };
    btn.disabled = true;
    btn.textContent = T("form.sending");
    post("/api/vote", body).then(function (res) {
      if (res.status === 201) {
        $("thanks-text").textContent = T("thanks.text", { name: nameOf(body.favourite) });
        show("thanks");
        $("thanks").focus();
        return;
      }
      if (res.status === 409) return alreadyVoted();
      if (res.status === 401) { signedOut(T("signin.err.expired")); return; }
      if (res.status === 429) retry("form.err.429");
      else if (res.status === 400) retry("form.err.400");
      else retry("form.err.other");
    }).catch(function () { retry("form.err.network"); });
  }

  // ---- email sign-in (one-time code) ----
  var siteKey = null, tsId = null, tsToken = null, tsReady = false, tsTimer = null;
  var email = "", resendTimer = null;

  window.onTurnstileReady = function () { tsReady = true; renderTurnstile(); };

  function renderTurnstile() {
    if (tsId !== null || !tsReady || !siteKey || $("signin").hidden) return;
    clearTimeout(tsTimer);
    tsId = window.turnstile.render("#turnstile", {
      sitekey: siteKey,
      theme: "auto",
      callback: function (t) { tsToken = t; $("ts-note").textContent = ""; },
      "expired-callback": function () { tsToken = null; },
      "error-callback": function () { tsToken = null; $("ts-note").textContent = T("signin.tsFailed"); },
    });
  }

  function resetTurnstile() {
    tsToken = null;
    if (tsId !== null && tsReady) window.turnstile.reset(tsId);
  }

  function step(n) {
    $("email-form").hidden = n !== 1;
    $("email-actions").hidden = n !== 1;
    $("email-errors").hidden = n !== 1;
    $("code-form").hidden = n !== 2;
    $("signin").classList.toggle("s2", n === 2);
    $("ts-label").textContent = T(n === 1 ? "signin.tsLabel1" : "signin.tsLabel2");
    $("signin-intro").textContent = T(n === 1 ? "signin.intro1" : "signin.intro2");
  }

  function startResendTimer() {
    clearInterval(resendTimer);
    var left = 60;
    var btn = $("resend");
    btn.disabled = true;
    var tick = function () {
      if (left <= 0) { clearInterval(resendTimer); btn.disabled = false; btn.textContent = T("signin.resend"); return; }
      btn.textContent = T("signin.resendWait", { n: left });
      left -= 1;
    };
    tick();
    resendTimer = setInterval(tick, 1000);
  }

  function signedOut(note) {
    $("who").hidden = true;
    $("admin").hidden = true;
    show("signin");
    step(1);
    boxMsg("email-errors", note || "");
    if (!tsReady) {
      clearTimeout(tsTimer);
      tsTimer = setTimeout(function () {
        if (!tsReady) $("ts-note").textContent = T("signin.tsTimeout");
      }, 8000);
    }
    renderTurnstile();
  }

  function requestCode(isResend) {
    var errId = isResend ? "code-errors" : "email-errors";
    boxMsg(errId, "");
    if (!tsToken) {
      boxMsg(errId, T("signin.err.noToken"));
      return;
    }
    var btn = isResend ? $("resend") : $("email-send");
    var label = btn.textContent;
    btn.disabled = true;
    post("/api/otp/request", { email: email, turnstile: tsToken, locale: window.i18n.locale }).then(function (res) {
      resetTurnstile();
      if (res.ok) {
        $("code-hint").textContent = "";
        $("code-hint").appendChild(window.i18n.node("signin.codeHint", { email: h("strong", { text: email }) }));
        step(2);
        $("code-input").value = "";
        boxMsg("code-errors", "");
        startResendTimer();
        $("code-input").focus();
        $("email-send").disabled = false;
        return;
      }
      btn.disabled = false;
      btn.textContent = label;
      boxMsg(errId, T(res.status === 429 ? "signin.err.otp429"
        : res.status === 400 ? "signin.err.otp400"
        : res.status === 503 ? "signin.err.otp503"
        : "signin.err.otpOther"));
      if (isResend) startResendTimer();
    }).catch(function () {
      resetTurnstile();
      btn.disabled = false;
      btn.textContent = label;
      boxMsg(errId, T("signin.err.network"));
    });
  }

  function onEmailSubmit(ev) {
    ev.preventDefault();
    var v = $("email-input").value.trim();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v)) {
      boxMsg("email-errors", T("signin.err.emailInvalid"));
      $("email-input").focus();
      return;
    }
    email = v;
    requestCode(false);
  }

  function onCodeSubmit(ev) {
    ev.preventDefault();
    var code = $("code-input").value.replace(/\s+/g, "");
    if (!/^\d{6}$/.test(code)) {
      boxMsg("code-errors", T("signin.err.codeFormat"));
      $("code-input").focus();
      return;
    }
    var btn = $("code-send");
    btn.disabled = true;
    boxMsg("code-errors", "");
    post("/api/otp/verify", { email: email, code: code }).then(function (res) {
      btn.disabled = false;
      if (res.ok) { load(); return; }
      boxMsg("code-errors", T(res.status === 429 ? "signin.err.code429" : res.status === 400 ? "signin.err.code400" : "signin.err.codeOther"));
      $("code-input").focus();
    }).catch(function () {
      btn.disabled = false;
      boxMsg("code-errors", T("signin.err.network"));
    });
  }

  function load() {
    show("loading");
    Promise.all([
      getJSON("/api/me"),
      getJSON("/api/options").then(window.i18n.localizeOptions),
      getJSON("/api/config").catch(function () { return {}; }),
    ]).then(function (v) {
      var me = v[0];
      options = Array.isArray(v[1]) ? v[1] : [];
      config = v[2] || {};
      siteKey = config.turnstile_sitekey || null;
      renderPrivacy();
      $("admin").hidden = !me.is_admin;
      if (!me.email) return signedOut("");
      $("who-text").textContent = "";
      $("who-text").appendChild(window.i18n.node("pool.signedIn", { email: h("strong", { text: me.email }) }));
      $("who").hidden = false;
      if (me.voted) return alreadyVoted();
      if (!options.length) throw new Error("no options");
      render();
      restoreDraft();
      updateCounter();
      show("poll");
    }).catch(function () { show("load-error"); });
  }

  window.i18n.ready.then(function () {
    window.i18n.beforeChange(saveDraft);
    var link = h("a", { href: "/licenses/Inter-OFL-1.1.txt", text: T("common.licences") });
    $("foot").appendChild(window.i18n.node("common.footer", { link: link }));
    $("poll").addEventListener("submit", submit);
    $("comment").addEventListener("input", updateCounter);
    $("email-form").addEventListener("submit", onEmailSubmit);
    $("code-form").addEventListener("submit", onCodeSubmit);
    $("resend").addEventListener("click", function () { requestCode(true); });
    $("change").addEventListener("click", function () {
      clearInterval(resendTimer);
      step(1);
      boxMsg("code-errors", "");
      $("email-input").focus();
    });
    $("signout").addEventListener("click", function () {
      post("/api/logout", {}).catch(function () {}).then(function () {
        email = "";
        $("email-input").value = "";
        load();
      });
    });
    $("retry").addEventListener("click", load);
    load();
  });
})();
