// Post-build edits on the Vite output (dist/), then a check that no forbidden name is left.
// usage: node postbuild.mjs <dist-dir>
import fs from "node:fs";
import path from "node:path";

const dist = process.argv[2];
const walk = (d) => fs.readdirSync(d, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? walk(path.join(d, e.name)) : [path.join(d, e.name)]));

// 1. Fonts: keep woff2 only (every browser that can run the UI has it).
for (const f of walk(dist)) {
  if (f.endsWith(".woff")) fs.rmSync(f);
  if (f.endsWith(".css")) {
    const css = fs.readFileSync(f, "utf8").replace(/,\s*url\([^)]*\.woff\)\s*format\("woff"\)/g, "");
    fs.writeFileSync(f, css);
  }
}

// 2. Vendor names that sample data or the bundled catalogues would carry.
const swaps = [
  ["Google Meet", "Meet"],
  ["meet.google.com", "meet.example.org"],
  ["Google Calendar", "Calendar account"],
  ["Google Agenda", "Agenda"],
  ["Google:", "Account:"],
  ["googleName", "accountName"],
  ["kindGoogle", "kindAccount"],
  // Catalogues in other languages carry the vendor name inside their own words ("Google カレンダー"); the sample data has no such account.
  ["Google", "Account"],
];
for (const f of walk(dist).filter((f) => f.endsWith(".js"))) {
  let s = fs.readFileSync(f, "utf8");
  for (const [a, b] of swaps) s = s.split(a).join(b);
  fs.writeFileSync(f, s);
}

// 3. Page wiring: the shim before the app, and the shim's stylesheet on the settings page.
for (const page of ["app.html", "interrupt.html"]) {
  const f = path.join(dist, page);
  let h = fs.readFileSync(f, "utf8");
  const css = page === "app.html" ? '    <link rel="stylesheet" href="/preview/shim.css">\n' : "";
  h = h.replace(/(\s*)<script type="module"/, `$1<script src="/preview/shim.js"></script>\n${css}    <script type="module"`);
  fs.writeFileSync(f, h);
}

// 4. Guards: no inline script, no forbidden name.
const bad = /claude|fable|gemini|google|anthropic|nano banana|argon/i;
for (const f of walk(dist)) {
  const s = fs.readFileSync(f, "utf8");
  if (bad.test(s)) throw new Error(`forbidden name in ${f}`);
  if (f.endsWith(".html") && /<script(?![^>]*\bsrc=)[^>]*>/i.test(s)) throw new Error(`inline script in ${f}`);
}
console.log("postbuild ok");
