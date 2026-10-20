// Fails (exit 1) when a locale file misses a key, has a different placeholder set, breaks a copy rule,
// or when a page uses a key that en-US does not define.   usage: node check-i18n.mjs [web-dir [locale]]
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const web = path.resolve(process.argv[2] || path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "web"));
const ALL = ["pt-BR", "de", "en-US", "es", "ar", "tr", "hi", "zh-CN", "ja"];
// Optional third argument: check en-US plus one locale only.
const LOCALES = process.argv[3] ? ["en-US", process.argv[3]] : ALL;
const PLURAL = /^results\.(votes|comments)\.(zero|one|two|few|many|other)$/;
const read = (f) => JSON.parse(fs.readFileSync(path.join(web, f), "utf8"));
const errors = [];
const err = (m) => errors.push(m);
const holders = (s) => [...String(s).matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join(",");

const dicts = Object.fromEntries(LOCALES.map((l) => [l, read(`i18n/${l}.json`)]));
const base = dicts["en-US"];

const FORBIDDEN_NAMES = /claude|fable|gemini|google|anthropic|nano banana|argon/i;
const rules = {
  "en-US": [/\b(favour\w*|colour\w*|licence\w*|centre\w*)\b/i, "British spelling in en-US"],
  "pt-BR": [/\b(ecrã|ecrãs|ficheiro\w*|telemóvel|telemóveis|utilizador\w*|aplicação|aplicações|carateres)\b/i, "European Portuguese word in pt-BR"],
  de: [/\b(Sie|Ihre[nmrs]?|Ihr)\b/, "formal Sie in de (use du)"],
  es: [/\b(usted|ustedes|vosotros)\b/i, "usted or vosotros in es (use tú)"],
  ar: [/[,?]/, "ASCII comma or question mark in ar (use ، and ؟)"],
  zh: [/[,?]/, "ASCII comma or question mark in zh-CN (use full-width punctuation)"],
  ja: [/[,?]/, "ASCII comma or question mark in ja (use full-width punctuation)"],
};

function copyRules(where, locale, text) {
  if (/[—–]/.test(text)) err(`${where}: dash character`);
  if (/!/.test(text)) err(`${where}: exclamation mark`);
  if (/\p{Extended_Pictographic}/u.test(text)) err(`${where}: emoji`);
  if (FORBIDDEN_NAMES.test(text)) err(`${where}: forbidden name`);
  const rule = rules[locale] || rules[locale.split("-")[0]];
  if (rule && rule[0].test(text)) err(`${where}: ${rule[1]} (${text.match(rule[0])[0]})`);
  if (!String(text).trim()) err(`${where}: empty`);
}

for (const l of LOCALES) {
  const d = dicts[l];
  const fam = (k) => (PLURAL.test(k) ? k.replace(/\.\w+$/, "") : k);
  const baseFam = new Set(Object.keys(base).map(fam));
  for (const k of Object.keys(base)) {
    if (PLURAL.test(k)) {
      if (!((fam(k) + ".other") in d)) err(`${l}: missing plural ${fam(k)}.other`);
      continue;
    }
    if (!(k in d)) err(`${l}: missing key ${k}`);
    else if (holders(d[k]) !== holders(base[k])) err(`${l}: placeholders of ${k} differ (${holders(d[k])} vs ${holders(base[k])})`);
  }
  for (const k of Object.keys(d)) {
    if (!baseFam.has(fam(k))) err(`${l}: extra key ${k}`);
    else if (PLURAL.test(k) && holders(d[k]) !== "n" && !/\.(zero|one|two)$/.test(k)) err(`${l}: plural ${k} must use {n}`);
  }
  for (const [k, v] of Object.entries(d)) copyRules(`${l} ${k}`, l, v);
}

// Option texts: same ids, same fields, same number of history steps as the contract file.
const contract = read("options.json");
for (const l of LOCALES) {
  const m = read(`options.${l}.json`);
  for (const o of contract) {
    const x = m[o.id];
    if (!x) { err(`options.${l}.json: missing ${o.id}`); continue; }
    for (const f of ["name", "tagline", "story"]) if (typeof x[f] !== "string" || !x[f]) err(`options.${l}.json ${o.id}: missing ${f}`);
    if (!Array.isArray(x.history) || x.history.length !== o.history.length) err(`options.${l}.json ${o.id}: history length`);
    [x.name, x.tagline, x.story, ...(x.history || [])].forEach((s, i) => copyRules(`options.${l}.json ${o.id}#${i}`, l, String(s)));
  }
  for (const id of Object.keys(m)) if (!contract.some((o) => o.id === id)) err(`options.${l}.json: unknown id ${id}`);
}

// Keys used by the pages must exist.
const used = new Set();
const keyRe = /["']([a-z][\w]*\.[\w.]+)["']/g;
for (const f of fs.readdirSync(web).concat(fs.readdirSync(path.join(web, "preview")).map((x) => `preview/${x}`))) {
  if (!/\.(html|js)$/.test(f) || f.startsWith("preview/assets")) continue;
  const s = fs.readFileSync(path.join(web, f), "utf8");
  for (const m of s.matchAll(/data-i18n="([\w.]+)"/g)) used.add([m[1], f]);
  for (const m of s.matchAll(/data-i18n-attr="([^"]+)"/g)) for (const p of m[1].split(";")) used.add([p.split("=")[1]?.trim(), f]);
  if (/(pool|results|landing|preview)\.js$/.test(f)) for (const m of s.matchAll(/\bT\("([\w.]+)"/g)) used.add([m[1], f]);
  if (f === "i18n.js") continue;
}
for (const [k, f] of used) {
  if (!k || k.endsWith(".")) continue;
  if (!(k in base) && !((k + ".other") in base)) err(`${f}: uses unknown key ${k}`);
}
// Keys built at run time (prefix + value), listed here so a rename is caught.
for (const k of ["form.love", "form.ok", "form.no", "pv.msg.join", "pv.msg.open_event", "pv.msg.snooze", "pv.msg.dismiss", "pv.msg.already_there",
  "privacy.processor.cloudflare", "privacy.processor.resend", "results.votes.one", "results.votes.other", "results.comments.one", "results.comments.other"]) {
  if (!(k in base)) err(`dynamic key missing: ${k}`);
}

if (errors.length) { console.error(errors.join("\n")); console.error(`\n${errors.length} problem(s)`); process.exit(1); }
console.log(`i18n ok: ${Object.keys(base).length} keys x ${LOCALES.length} locales, ${contract.length} options x ${LOCALES.length}`);
