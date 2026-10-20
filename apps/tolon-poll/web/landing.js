(function () {
  "use strict";

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
  function asset(p) { return "/" + String(p).replace(/^\/+/, ""); }
  // 32 px glyph as the 2x source; same folder, 32.png next to 16.png.
  function srcset(small) { return asset(small) + " 1x, " + asset(small.replace(/16\.png$/, "32.png")) + " 2x"; }

  function card(o) {
    var t = window.i18n.t;
    return h("li", { class: "card" },
      h("div", { class: "card-head" },
        h("img", { class: "card-icon", src: asset(o.images.icon), width: "112", height: "112", alt: t("card.iconAlt", { name: o.name }) }),
        h("div", {},
          h("h3", { class: "card-name", text: o.name }),
          h("p", { class: "tagline", text: o.tagline }),
          h("p", { class: "menubar" },
            h("span", { class: "bar" }, h("img", { src: asset(o.images.small), srcset: srcset(o.images.small), width: "16", height: "16", alt: t("card.smallAlt", { name: o.name }) })),
            h("span", { text: t("card.menubar") })))),
      h("p", { class: "story", text: o.story }));
  }

  window.i18n.ready.then(function () {
    var link = document.createElement("a");
    link.href = "/licenses/Inter-OFL-1.1.txt";
    link.textContent = window.i18n.t("common.licences");
    document.getElementById("foot").appendChild(window.i18n.node("common.footer", { link: link }));
    return fetch("/api/options", { headers: { Accept: "application/json" } })
      .then(function (r) { if (!r.ok) throw new Error("options"); return r.json(); })
      .then(window.i18n.localizeOptions)
      .then(function (list) {
        var ul = document.getElementById("gallery");
        list.forEach(function (o) { ul.appendChild(card(o)); });
      });
  }).catch(function () { document.getElementById("gallery-error").hidden = false; });
})();
