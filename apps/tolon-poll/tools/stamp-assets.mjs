// Adds ?v=<hash> to every local stylesheet and script in the HTML files, so a changed file gets a new URL.
// Cloudflare keeps css and js at the edge for hours; without this, a new page can run with an old style.css.
// The hash covers every css, js and json file (not the HTML, and not preview/assets, whose names are hashed).
// i18n.js reads its own ?v= and passes it on to the language and option files it fetches.
// usage: node stamp-assets.mjs [web-dir]   (safe to run again)
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const web = path.resolve(process.argv[2] || path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "web"));
const walk = (d) => fs.readdirSync(d, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? walk(path.join(d, e.name)) : [path.join(d, e.name)]));
const files = walk(web).sort();
const hash = crypto.createHash("sha256");
for (const f of files) {
  const rel = path.relative(web, f);
  if (rel.startsWith(`preview${path.sep}assets`) || !/\.(css|js|json)$/.test(rel)) continue;
  hash.update(rel).update(fs.readFileSync(f));
}
const v = hash.digest("hex").slice(0, 10);
let changed = 0;
for (const f of files.filter((f) => f.endsWith(".html"))) {
  const before = fs.readFileSync(f, "utf8");
  const after = before.replace(/(\b(?:href|src)=")(\/(?!preview\/assets\/)[^"?#]+\.(?:css|js))(?:\?v=[0-9a-f]+)?(")/g, `$1$2?v=${v}$3`);
  if (after !== before) { fs.writeFileSync(f, after); changed++; }
}
console.log(`stamped ${v} (${changed} html file(s) changed)`);
