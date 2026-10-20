(function () {
  "use strict";

  var MESSAGES = {
    join: "Join pressed. In the real app this opens your meeting link.",
    open_event: "Open the event pressed. In the real app this opens the event page.",
    snooze: "Snooze pressed. In the real app the reminder closes and comes back in 5 minutes.",
    dismiss: "Dismiss pressed. In the real app this silences the reminder on this computer.",
    already_there: "Already there pressed. In the real app this marks the meeting as attended and closes the reminder.",
  };

  var params = new URLSearchParams(window.location.search);
  var options = [];

  function $(id) { return document.getElementById(id); }
  function asset(p) { return "/" + String(p).replace(/^\/+/, ""); }

  function getJSON(url) {
    return fetch(url, { headers: { Accept: "application/json" }, credentials: "same-origin" }).then(function (r) {
      if (!r.ok) throw new Error(url);
      return r.json();
    });
  }

  function choose(id) {
    var o = options.filter(function (x) { return x.id === id; })[0];
    if (!o) return;
    var q = "?icon=" + encodeURIComponent(o.id);
    $("show").href = "/preview/interrupt.html" + q;
    $("cal-link").href = "/preview/app.html" + q + "#/calendars";
    $("tray").src = asset(o.images.small);
    $("tray").srcset = asset(o.images.small) + " 1x, " + asset(o.images.small.replace(/16\.png$/, "32.png")) + " 2x";
    $("dock").src = asset(o.images.icon);
    $("scene").src = asset(o.images.scene);
    $("scene").alt = "The Tolon reminder screen drawn in the " + o.name + " style";
    $("scene-cap").textContent = o.name + ": " + o.tagline;
    $("desk-cap").textContent = "A fake desktop with " + o.name + " at 16 px in the menu bar and at Dock size at the bottom.";
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

  var done = params.get("done");
  if (done && MESSAGES[done]) {
    var p = document.createElement("p");
    p.textContent = MESSAGES[done];
    $("status").appendChild(p);
  }

  getJSON("/options.json").catch(function () { return getJSON("/api/options"); }).then(function (list) {
    options = list;
    var want = params.get("icon");
    var first = options.some(function (o) { return o.id === want; }) ? want : options[0].id;
    buildChooser(first);
    choose(first);
  }).catch(function () { $("load-error").hidden = false; });
})();
