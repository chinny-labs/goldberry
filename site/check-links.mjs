// Fails if a page in site/_site links to a file or #anchor on the site that
// isn't there. Links off the site (GitHub, elsewhere) aren't checked.
//
//   node check-links.mjs
import { existsSync } from "node:fs";
import { readdir, readFile } from "node:fs/promises";
import { dirname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const out = join(dirname(fileURLToPath(import.meta.url)), "_site");
const pages = (await readdir(out, { recursive: true })).filter((f) => f.endsWith(".html")).map((f) => join(out, f));
const ids = new Map();
const html = new Map();
for (const p of pages) {
  const s = await readFile(p, "utf8");
  html.set(p, s);
  ids.set(p, new Set([...s.matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])));
}

const unescape = (s) => s.replace(/&amp;/g, "&").replace(/&quot;/g, '"').replace(/&lt;/g, "<").replace(/&gt;/g, ">");
let bad = 0;
for (const [p, s] of html) {
  for (const [, raw] of s.matchAll(/\s(?:href|src|srcset)="([^"]+)"/g)) {
    const link = unescape(raw);
    if (/^[a-z]+:/i.test(link)) continue;
    const [path, frag] = link.split("#");
    const target = path ? normalize(join(dirname(p), decodeURIComponent(path))) : p;
    const where = p.slice(out.length + 1);
    if (!existsSync(target)) {
      console.error(`${where}: no such file: ${link}`);
      bad++;
    } else if (frag && ids.has(target) && !ids.get(target).has(frag)) {
      console.error(`${where}: no #${frag} in ${link}`);
      bad++;
    }
  }
}
if (bad) {
  console.error(`${bad} broken link(s)`);
  process.exit(1);
}
console.log(`links ok across ${pages.length} pages`);
