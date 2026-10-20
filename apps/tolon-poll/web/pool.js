(function () {
  "use strict";

  var MAX = 1000;
  var REACTIONS = [["love", "Love it"], ["ok", "It is fine"], ["no", "Not for me"]];
  var options = [];

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

  // 32 px glyph as the 2x source; same folder, 32.png next to 16.png.
  function srcset(small) { return asset(small) + " 1x, " + asset(small.replace(/16\.png$/, "32.png")) + " 2x"; }

  function asset(path) { return "/" + String(path).replace(/^\/+/, ""); }

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

  function card(o) {
    var more = function (title, body) {
      var d = h("details", { class: "more" }, h("summary", { text: title }));
      d.appendChild(body);
      return d;
    };
    var steps = h("ol");
    (o.history || []).forEach(function (s) { steps.appendChild(h("li", { text: s })); });

    var react = h("fieldset", { class: "react" }, h("legend", { text: "Your reaction to " + o.name }));
    var opts = h("div", { class: "opts" });
    REACTIONS.forEach(function (r) {
      opts.appendChild(radio("react-" + o.id, "react-" + o.id + "-" + r[0], r[0], r[1], "opt"));
    });
    react.appendChild(opts);

    return h("li", { class: "card", "data-id": o.id },
      h("div", { class: "card-head" },
        h("img", { class: "card-icon", src: asset(o.images.icon), width: "112", height: "112", alt: o.name + " app icon" }),
        h("div", {},
          h("h3", { class: "card-name", text: o.name }),
          h("p", { class: "tagline", text: o.tagline }),
          h("p", { class: "menubar" },
            h("span", { class: "bar" }, h("img", { src: asset(o.images.small), srcset: srcset(o.images.small), width: "16", height: "16", alt: o.name + " at 16 pixels" })),
            h("span", { text: "In your menu bar, at real size" })))),
      more("Read the story", h("p", { text: o.story })),
      more("How the concept evolved", steps),
      h("figure", { class: "scene" },
        h("img", { src: asset(o.images.scene), width: "1280", height: "800", loading: "lazy", alt: "The Tolon takeover screen drawn in the " + o.name + " style" }),
        h("figcaption", { text: "The screen Tolon shows before a meeting, in this style" })),
      h("div", { class: "choices" },
        radio("favourite", "fav-" + o.id, o.id, "My favourite: " + o.name, "fav"),
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

  function setErrors(msg) {
    var box = $("errors");
    box.textContent = "";
    if (msg) box.appendChild(h("p", { text: msg }));
  }

  function retryable(msg) {
    var btn = $("send");
    btn.disabled = false;
    btn.textContent = "Send my vote";
    setErrors(msg);
    btn.focus();
  }

  function updateCounter() {
    var n = $("comment").value.length;
    var c = $("counter");
    c.textContent = n + " of " + MAX + " characters";
    c.className = "counter" + (MAX - n <= 50 ? " low" : "");
  }

  function alreadyVoted() {
    show("voted");
    $("voted").focus();
  }

  function submit(ev) {
    ev.preventDefault();
    setErrors("");
    var fav = document.querySelector('input[name="favourite"]:checked');
    if (!fav) {
      setErrors("Choose your favourite icon first. Use the My favourite button on one of the cards.");
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
    btn.disabled = true;
    btn.textContent = "Sending";
    post("/api/vote", body).then(function (res) {
      if (res.status === 201) {
        $("thanks-text").textContent = "You picked " + nameOf(body.favourite) + ". You can close this page.";
        show("thanks");
        $("thanks").focus();
        return;
      }
      if (res.status === 409) return alreadyVoted();
      if (res.status === 401) { signedOut("Your sign-in has expired. Confirm your email again to send your vote."); return; }
      if (res.status === 429) retryable("You are sending requests too quickly. Wait a minute, then press Send my vote again.");
      else if (res.status === 400) retryable("The form was not accepted. Check that you picked a favourite and that your comment is no longer than 1000 characters.");
      else retryable("We could not save your vote. Your choices are still here. Try again in a moment, and tell Daniel if it keeps failing.");
    }).catch(function () {
      retryable("We could not reach the server. Check your connection and press Send my vote again. Your choices are still here.");
    });
  }

  // ---- email sign-in (one-time code) ----
  var siteKey = null, tsId = null, tsToken = null, tsReady = false, tsTimer = null;
  var email = "", resendTimer = null;

  function field(id) { return $(id); }
  function boxMsg(id, msg) {
    var box = $(id);
    box.textContent = "";
    if (msg) box.appendChild(h("p", { text: msg }));
  }

  window.onTurnstileReady = function () { tsReady = true; renderTurnstile(); };

  function renderTurnstile() {
    if (tsId !== null || !tsReady || !siteKey || $("signin").hidden) return;
    clearTimeout(tsTimer);
    tsId = window.turnstile.render("#turnstile", {
      sitekey: siteKey,
      theme: "auto",
      callback: function (t) { tsToken = t; $("ts-note").textContent = ""; },
      "expired-callback": function () { tsToken = null; },
      "error-callback": function () { tsToken = null; $("ts-note").textContent = "The security check could not run. Reload the page or try another browser."; },
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
    $("ts-label").textContent = n === 1 ? "Security check" : "Security check, needed to send a new code";
    $("signin-intro").textContent = n === 1
      ? "Enter your email address. We send a 6-digit code to it, so each person can vote once."
      : "Check your inbox, then enter the code below.";
  }

  function startResendTimer() {
    clearInterval(resendTimer);
    var left = 60;
    var btn = $("resend");
    btn.disabled = true;
    var tick = function () {
      if (left <= 0) { clearInterval(resendTimer); btn.disabled = false; btn.textContent = "Send a new code"; return; }
      btn.textContent = "Send a new code (available in " + left + " s)";
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
        if (!tsReady) $("ts-note").textContent = "The security check did not load. Check your connection, turn off any content blocker for this page, and reload.";
      }, 8000);
    }
    renderTurnstile();
  }

  function requestCode(isResend) {
    var errId = isResend ? "code-errors" : "email-errors";
    boxMsg(errId, "");
    if (!tsToken) {
      boxMsg(errId, "Complete the security check first, then try again.");
      return;
    }
    var btn = isResend ? $("resend") : $("email-send");
    var label = btn.textContent;
    btn.disabled = true;
    post("/api/otp/request", { email: email, turnstile: tsToken }).then(function (res) {
      resetTurnstile();
      if (res.status === 202 || res.ok) {
        $("sent-to").textContent = email;
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
      var msg = res.status === 429 ? "Too many codes requested. Wait a few minutes, then try again."
        : res.status === 400 ? "That email address or security check was not accepted. Check the address and complete the check again."
        : res.status === 503 ? "Email sign-in is not available right now. Try again later."
        : "We could not send the code. Try again in a moment.";
      boxMsg(errId, msg);
      if (isResend) startResendTimer();
    }).catch(function () {
      resetTurnstile();
      btn.disabled = false;
      btn.textContent = label;
      boxMsg(errId, "We could not reach the server. Check your connection and try again.");
    });
  }

  function onEmailSubmit(ev) {
    ev.preventDefault();
    var v = $("email-input").value.trim();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v)) {
      boxMsg("email-errors", "Enter a valid email address, for example name@company.com.");
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
      boxMsg("code-errors", "Enter the 6 digits from the email.");
      $("code-input").focus();
      return;
    }
    var btn = $("code-send");
    btn.disabled = true;
    boxMsg("code-errors", "");
    post("/api/otp/verify", { email: email, code: code }).then(function (res) {
      btn.disabled = false;
      if (res.ok) { load(); return; }
      var msg = res.status === 429 ? "Too many wrong codes. Wait a few minutes, or send a new code."
        : res.status === 400 ? "That code is not right or has expired. Check the digits, or send a new code."
        : "We could not check the code. Try again in a moment.";
      boxMsg("code-errors", msg);
      $("code-input").focus();
    }).catch(function () {
      btn.disabled = false;
      boxMsg("code-errors", "We could not reach the server. Check your connection and try again.");
    });
  }

  function load() {
    show("loading");
    Promise.all([getJSON("/api/me"), getJSON("/api/options"), getJSON("/api/config").catch(function () { return {}; })]).then(function (v) {
      var me = v[0];
      options = Array.isArray(v[1]) ? v[1] : [];
      siteKey = v[2].turnstile_sitekey || null;
      $("admin").hidden = !me.is_admin;
      if (!me.email) return signedOut("");
      $("email").textContent = me.email;
      $("who").hidden = false;
      if (me.voted) return alreadyVoted();
      if (!options.length) throw new Error("no options");
      render();
      updateCounter();
      show("poll");
    }).catch(function () { show("load-error"); });
  }

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
  $("poll").addEventListener("submit", submit);
  $("comment").addEventListener("input", updateCounter);
  $("retry").addEventListener("click", load);
  load();
})();
