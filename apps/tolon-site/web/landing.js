(function () {
  "use strict";

  function getJSON(url) {
    return fetch(url + window.i18n.vq, { headers: { Accept: "application/json" } }).then(function (r) {
      if (!r.ok) throw new Error(url);
      return r.json();
    });
  }

  window.i18n.ready.then(function () {
    var link = document.createElement("a");
    link.href = "/licenses/Inter-OFL-1.1.txt";
    link.textContent = window.i18n.t("common.licences");
    document.getElementById("foot").appendChild(window.i18n.node("common.footer", { link: link }));
    // The English page is already in index.html; load the data again only to show it in another language.
    if (window.i18n.locale === "en-US") return;
    return Promise.all([getJSON("/results.json"), getJSON("/options.json").then(window.i18n.localizeOptions)]).then(function (v) {
      document.getElementById("results").innerHTML = window.renderResults(v[0], v[1], window.i18n.t);
    });
  }).catch(function () { /* the English page stays */ });
})();
