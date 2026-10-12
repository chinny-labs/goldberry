// Builds the GitHub Pages site into site/_site: the landing page from
// site/src, and the Markdown in docs/ rendered as HTML pages with the same
// look. Links between rendered docs stay on the site; links to anything
// else in the repo go to GitHub.
//
//   cd site && npm ci && node build.mjs
import { Marked, Renderer } from "marked";
import { cp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, join, posix, relative } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, "..");
const out = join(here, "_site");
const blob = "https://github.com/chinny-labs/goldberry/blob/main/";
const tree = "https://github.com/chinny-labs/goldberry/tree/main/";

// The docs on the site, in sidebar order. Paths are relative to the repo.
const sections = [
  {
    title: "Run it",
    pages: [
      "docs/operations/install.md",
      "docs/operations/upgrade.md",
      "docs/operations/backup-restore.md",
      "docs/operations/investing.md",
      "docs/operations/troubleshooting.md",
    ],
  },
  {
    title: "API and AI tools",
    pages: ["docs/operations/api.md", "docs/operations/api-tokens.md", "docs/operations/mcp.md"],
  },
  { title: "Design", pages: ["docs/design/plan.md", "docs/adr/README.md"] },
];
const adrs = (await readFile(join(repo, "docs/adr/README.md"), "utf8"))
  .matchAll(/\]\((\d{4}-[^)]+\.md)\)/g)
  .map((m) => "docs/adr/" + m[1])
  .toArray();
const rendered = new Set([...sections.flatMap((s) => s.pages), ...adrs]);

// docs/x/y.md → docs/x/y.html; a README is its folder's index.
const htmlPath = (md) => md.replace(/README\.md$/, "index.md").replace(/\.md$/, ".html");

// GitHub's heading anchors, so links like plan.md#14-phasing keep working.
function slugger() {
  const seen = new Map();
  return (text) => {
    let s = text
      .toLowerCase()
      .replace(/<[^>]+>/g, "")
      .replace(/[^\p{L}\p{N}\s_-]/gu, "")
      .trim()
      .replace(/\s/g, "-");
    const n = seen.get(s) ?? 0;
    seen.set(s, n + 1);
    return n ? `${s}-${n}` : s;
  };
}

const escape = (s) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);

function render(md, src) {
  const slug = slugger();
  let title = "";
  const toc = [];
  const renderer = {
    heading({ tokens, depth, text }) {
      const id = slug(text);
      const inner = this.parser.parseInline(tokens);
      if (depth === 1 && !title) title = inner.replace(/<[^>]+>/g, "");
      if (depth === 2) toc.push({ id, html: inner });
      return `<h${depth} id="${id}"><a class="anchor" href="#${id}" aria-hidden="true">#</a>${inner}</h${depth}>\n`;
    },
    link({ href, title: t, tokens }) {
      return `<a href="${escape(rewrite(href, src))}"${t ? ` title="${escape(t)}"` : ""}>${this.parser.parseInline(tokens)}</a>`;
    },
    code({ text, lang }) {
      const cls = lang ? ` class="language-${escape(lang)}"` : "";
      const note = lang === "mermaid" ? `<p class="caption">A Mermaid diagram; <a href="${blob + src}">see it drawn on GitHub</a>.</p>` : "";
      return `<pre><code${cls}>${escape(text)}</code></pre>${note}\n`;
    },
    table(token) {
      return `<div class="table-wrap">${Renderer.prototype.table.call(this, token)}</div>`;
    },
  };
  const html = new Marked({ gfm: true, renderer }).parse(md);
  return { html, title, toc };
}

// rewrite points a link in src at the rendered page, or at GitHub.
function rewrite(href, src) {
  if (/^([a-z]+:|#|\/\/)/i.test(href)) return href;
  const [path, hash = ""] = href.split("#");
  const target = posix.normalize(posix.join(posix.dirname(src), path));
  const frag = hash ? "#" + hash : "";
  if (rendered.has(target)) return posix.relative(posix.dirname(htmlPath(src)), htmlPath(target)) + frag;
  return blob + target + frag;
}

const pageTitle = (p) => titles.get(p) ?? p;
const titles = new Map();

function layout({ root, title, body, nav, current, src, toc }) {
  const side = nav
    .map(
      (s) =>
        `<h2>${s.title}</h2><ul>${s.pages
          .map((p) => {
            const href = root + htmlPath(p);
            return `<li><a href="${href}"${p === current ? ' aria-current="page"' : ""}>${escape(pageTitle(p))}</a></li>`;
          })
          .join("")}</ul>`,
    )
    .join("");
  const onPage = toc.length > 1 ? `<nav class="toc" aria-label="On this page"><h2>On this page</h2><ul>${toc.map((t) => `<li><a href="#${t.id}">${t.html}</a></li>`).join("")}</ul></nav>` : "";
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${escape(title)} · Goldberry</title>
<meta name="description" content="Goldberry documentation: ${escape(title)}">
<link rel="icon" href="${root}icon.svg" type="image/svg+xml">
<link rel="stylesheet" href="${root}site.css">
</head>
<body class="docs">
<header class="top">
  <a class="brand" href="${root}index.html"><img src="${root}icon.svg" alt="" width="32" height="32">goldberry</a>
  <nav aria-label="Site"><a href="${root}index.html#features">Features</a><a href="${root}docs/index.html" aria-current="page">Docs</a><a href="https://github.com/chinny-labs/goldberry">GitHub</a></nav>
</header>
<div class="docs-shell">
  <main class="prose">
    <p class="jump"><a href="#all-docs">All docs ↓</a></p>
    ${body}
    <p class="edit"><a href="${src.endsWith(".md") ? blob + src : tree + src}">View this page on GitHub</a></p>
  </main>
  <nav class="docs-nav" id="all-docs" aria-label="Docs">${side}</nav>
  ${onPage}
</div>
${footer(root)}
</body>
</html>
`;
}

const footer = (root) => `<footer class="foot">
  <p><img src="${root}icon.svg" alt="" width="20" height="20"> Goldberry is free software under the <a href="${blob}LICENSE">AGPL-3.0</a>. Named for Goldberry, the River-daughter in <i>The Lord of the Rings</i>.</p>
  <p><a href="https://github.com/chinny-labs/goldberry">Source</a> · <a href="https://github.com/chinny-labs/goldberry/releases">Releases</a> · <a href="${root}docs/index.html">Docs</a></p>
</footer>`;

await rm(out, { recursive: true, force: true });
await mkdir(out, { recursive: true });

// Read every doc first so the sidebar can use their titles.
const docs = new Map();
for (const src of rendered) {
  const md = await readFile(join(repo, src), "utf8");
  const r = render(md, src);
  titles.set(src, src === "docs/adr/README.md" ? "Decisions (ADRs)" : r.title || src);
  docs.set(src, r);
}
const nav = [...sections, { title: "Decisions", pages: adrs }];

for (const [src, r] of docs) {
  const dest = join(out, htmlPath(src));
  const root = relative(dirname(dest), out).split("\\").join("/") + "/";
  await mkdir(dirname(dest), { recursive: true });
  await writeFile(dest, layout({ root, title: titles.get(src), body: r.html, nav, current: src, src, toc: r.toc }));
}

// The docs home: every section as cards.
const cards = nav
  .map(
    (s) =>
      `<section><h2>${s.title}</h2><ul class="doc-cards">${s.pages
        .map((p) => `<li><a href="${htmlPath(p).replace(/^docs\//, "")}">${escape(titles.get(p))}</a></li>`)
        .join("")}</ul></section>`,
  )
  .join("");
await writeFile(
  join(out, "docs/index.html"),
  layout({
    root: "../",
    title: "Documentation",
    body: `<h1>Documentation</h1><p class="lede">Installing and running Goldberry, its JSON API and MCP server, and the design behind it. New here? Start with <a href="operations/install.html">Install</a>.</p>${cards}`,
    nav,
    current: "",
    src: "docs",
    toc: [],
  }),
);

// The landing page and its assets.
await cp(join(here, "src"), out, { recursive: true });
await cp(join(repo, "docs/screenshots"), join(out, "screenshots"), { recursive: true });
await cp(join(repo, "internal/web/static/fonts"), join(out, "fonts"), { recursive: true });
await cp(join(repo, "internal/web/static/icon.svg"), join(out, "icon.svg"));
const index = await readFile(join(out, "index.html"), "utf8");
await writeFile(join(out, "index.html"), index.replace("<!-- footer -->", footer("")));
await writeFile(join(out, ".nojekyll"), "");
console.log(`built ${docs.size + 2} pages into ${relative(process.cwd(), out) || "."}`);
