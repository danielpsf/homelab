// Pre-renders the English results page into web/index.html from web/results.json and web/options.*.json, so the
// page shows the results without JavaScript. Run it after changing results.json, results.js or the en-US texts,
// then run stamp-assets.mjs.   usage: node build-results.mjs [web-dir]
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const web = path.resolve(process.argv[2] || path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "web"));
const { render } = createRequire(import.meta.url)(path.join(web, "results.js"));
const read = (f) => JSON.parse(fs.readFileSync(path.join(web, f), "utf8"));

const dict = read("i18n/en-US.json");
const T = (key, p) => String(dict[key] ?? key).replace(/\{(\w+)\}/g, (m, k) => (p && p[k] != null ? p[k] : m));
const texts = read("options.en-US.json");
const options = read("options.json").map((o) => ({ ...o, ...texts[o.id] }));

const file = path.join(web, "index.html");
const html = fs.readFileSync(file, "utf8");
const out = html.replace(/<!-- results:start -->[\s\S]*<!-- results:end -->/, () => `<!-- results:start -->${render(read("results.json"), options, T)}<!-- results:end -->`);
fs.writeFileSync(file, out);
console.log(`index.html: ${out.length} bytes`);
