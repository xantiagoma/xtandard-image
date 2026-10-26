// Pre-build step of the documentation site (ADR 0015): copies the repository's Markdown into
// src/content/docs/ (generated, gitignored) as Starlight pages. The files in the repository stay
// the single source of truth and keep rendering on GitHub; this step only adapts copies:
//
//   - the title, description and edit link come from the source: the H1 becomes `title` (and is
//     removed, with any logo line above it), the first paragraph becomes `description`, and
//     "Edit page" opens the source file on GitHub;
//   - relative links to files that are pages here (`CONFIG.md#x`, `../README.md`, `adr/0006-….md`,
//     `../clients/go/README.md`) become site routes under the base;
//   - relative links to other repository files and directories (scripts, Go sources, `deploy/…`)
//     become absolute GitHub URLs, and images become raw URLs (docs/assets is served by the site);
//   - a relative link to a file that does not exist fails the build;
//   - GitHub alerts (`> [!WARNING]`) become Starlight asides.
//
// Fenced code blocks and inline code spans are never rewritten. Anchors are checked after the
// build by starlight-links-validator (Markdown pages) and scripts/check-dist.ts (every HTML file).
//
//   bun scripts/sync.ts      (after `sh ../scripts/pages-build.sh ../dist/pages-static`)
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync } from "node:fs";
import { dirname, join, normalize, posix } from "node:path";
import { branch, image, pages, repoRoot, repoUrl, siteBase, type Page } from "../manifest.ts";

const siteDir = join(repoRoot, "site");
const outDir = join(siteDir, "src/content/docs");
const staticDir = join(repoRoot, "dist/pages-static");

const errors: string[] = [];
const fail = (msg: string) => errors.push(msg);

// ---- the schema major, from the files pages-build.sh assembled ------------------------------

const schemaDirs = existsSync(join(staticDir, "schema"))
  ? readdirSync(join(staticDir, "schema")).filter((d) => d !== "latest")
  : [];
if (schemaDirs.length !== 1 || !/^v[1-9][0-9]*$/.test(schemaDirs[0]!)) {
  console.error(`sync: expected one schema/v<N>/ in ${staticDir} (run \`bun run static\` first), found ${JSON.stringify(schemaDirs)}`);
  process.exit(1);
}
const major = schemaDirs[0]!;

// ---- routes ---------------------------------------------------------------------------------

const all = pages();
const route = (slug: string) => (slug === "" ? `${siteBase}/` : `${siteBase}/${slug}/`);
/** Repository file or directory (no trailing slash) → page slug. */
const routes = new Map<string, string>();
for (const p of all) if (p.src && !p.section) routes.set(p.src, p.slug);
routes.set("docs/adr", "project/adr");
routes.set("clients", "clients");
/** README.md is not a page (the home page replaces it); its sections map here. */
const readmeAnchors: Record<string, string> = {
  "": "",
  "quick-start": "#quick-start",
  disclaimer: "project/disclaimer/",
  "status-and-versioning": "reference/changelog/",
};

const isExternal = (t: string) => /^[a-z][a-z0-9+.-]*:/i.test(t) || t.startsWith("//");

/** Rewrites one link target found in `src`. `image` targets get a URL that serves the bytes. */
function rewrite(target: string, src: string, image: boolean, sectionOf?: string): string {
  // Absolute GitHub links to a file that is a page here (the client READMEs use them, because
  // they ship to registries) stay on the site.
  const blob = `${repoUrl}/blob/${branch}/`;
  if (target.startsWith(blob)) {
    const m = /^([^?#]*)(#.*)?$/.exec(target.slice(blob.length))!;
    const slug = routes.get(m[1]!);
    if (slug !== undefined && m[1] !== "README.md") return `${route(slug)}${m[2] ?? ""}`;
    return target;
  }
  if (target === "" || isExternal(target)) return target;
  if (target.startsWith("#")) {
    // A section page lost the rest of its document: send the anchor to the whole document.
    if (sectionOf) return rewrite(`${posix.basename(sectionOf)}${target}`, src, image);
    return target;
  }
  const m = /^([^?#]*)(\?[^#]*)?(#.*)?$/.exec(target)!;
  const pathPart = m[1]!;
  const hash = m[3] ?? "";
  let path: string;
  try {
    path = decodeURIComponent(pathPart);
  } catch {
    fail(`${src}: malformed link ${target}`);
    return target;
  }
  const resolved = posix.normalize(path.startsWith("/") ? path.slice(1) : posix.join(posix.dirname(src), path)).replace(/\/$/, "");
  if (resolved.startsWith("..")) {
    fail(`${src}: link ${target} leaves the repository`);
    return target;
  }
  const abs = join(repoRoot, resolved === "." ? "" : resolved);
  if (!existsSync(abs)) {
    fail(`${src}: broken link ${target} (${resolved} does not exist)`);
    return target;
  }
  if (resolved === "README.md") {
    const anchor = hash.slice(1);
    const to = readmeAnchors[anchor];
    if (to !== undefined) return to.startsWith("#") ? `${route("")}${to}` : `${siteBase}/${to}`.replace(/\/\/$/, "/");
    return `${repoUrl}${hash}`;
  }
  if (image) {
    if (resolved.startsWith("docs/assets/")) return `${siteBase}/assets/${resolved.slice("docs/assets/".length)}`;
    return `https://raw.githubusercontent.com/xantiagoma/xtandard-image/${branch}/${resolved}`;
  }
  const slug = routes.get(resolved);
  if (slug !== undefined) return `${route(slug)}${hash}`;
  const kind = statSync(abs).isDirectory() ? "tree" : "blob";
  return `${repoUrl}/${kind}/${branch}/${resolved}${m[2] ?? ""}${hash}`;
}

// ---- Markdown transforms --------------------------------------------------------------------

const fence = /^ {0,3}(`{3,}|~{3,})/;

/** Calls `fn` on each line outside fenced code blocks; code lines are kept as they are. */
function mapProse(lines: string[], fn: (line: string) => string): string[] {
  let open: string | null = null;
  return lines.map((line) => {
    const f = fence.exec(line);
    if (open) {
      if (f && f[1]![0] === open[0] && f[1]!.length >= open.length && line.trim() === f[1]) open = null;
      return line;
    }
    if (f) {
      open = f[1]!;
      return line;
    }
    return fn(line);
  });
}

/**
 * Replaces the inline code spans of a line with placeholders (CommonMark: a run of n backticks
 * opens a span that the next run of exactly n backticks closes; an unmatched run is literal).
 */
function maskCode(line: string, spans: string[]): string {
  const runs = [...line.matchAll(/`+/g)].map((m) => ({ at: m.index, len: m[0].length }));
  let out = "";
  let pos = 0;
  for (let i = 0; i < runs.length; i++) {
    const open = runs[i]!;
    if (open.at < pos) continue;
    const close = runs.findIndex((r, j) => j > i && r.len === open.len);
    if (close < 0) continue;
    const end = runs[close]!.at + open.len;
    out += `${line.slice(pos, open.at)}\u0000${spans.push(line.slice(open.at, end)) - 1}\u0000`;
    pos = end;
    i = close;
  }
  return out + line.slice(pos);
}

/** Rewrites the link targets on one prose line, leaving inline code spans alone. */
function rewriteLine(line: string, src: string, sectionOf?: string): string {
  const spans: string[] = [];
  let text = maskCode(line, spans);
  // Inline links and images: [text](target "title"), ![alt](<target>)
  text = text.replace(/(!?)\[((?:[^\[\]]|\[[^\]]*\])*)\]\(\s*(<[^>]*>|[^)\s]+)((?:\s+"[^"]*")?\s*)\)/g, (_s, bang, label, t, title) => {
    const bare = t.startsWith("<") ? t.slice(1, -1) : t;
    const out = rewrite(bare, src, bang === "!", sectionOf);
    return `${bang}[${label}](${t.startsWith("<") ? `<${out}>` : out}${title})`;
  });
  // Reference definitions: [label]: target
  text = text.replace(/^( {0,3}\[[^\]]+\]:\s*)(\S+)/, (_s, head, t) => head + rewrite(t, src, false, sectionOf));
  // Raw HTML attributes.
  text = text.replace(/\b(href|src)="([^"]*)"/g, (_s, attr, t) => `${attr}="${rewrite(t, src, attr === "src", sectionOf)}"`);
  text = text.replace(/\bsrcset="([^"]*)"/g, (_s, set: string) =>
    `srcset="${set.split(",").map((c) => c.trim().replace(/^\S+/, (u) => rewrite(u, src, true, sectionOf))).join(", ")}"`,
  );
  return text.replace(/\u0000(\d+)\u0000/g, (_s, i) => spans[Number(i)]!);
}

const alertKinds: Record<string, string> = { NOTE: "note", TIP: "tip", IMPORTANT: "note", WARNING: "caution", CAUTION: "danger" };

/** GitHub alerts (`> [!WARNING]` + quoted lines) → Starlight asides (`:::caution`). */
function alerts(lines: string[]): string[] {
  const out: string[] = [];
  for (let i = 0; i < lines.length; i++) {
    const m = /^> \[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*$/.exec(lines[i]!);
    if (!m) {
      out.push(lines[i]!);
      continue;
    }
    out.push(`:::${alertKinds[m[1]!]}`);
    while (i + 1 < lines.length && /^>/.test(lines[i + 1]!)) out.push(lines[++i]!.replace(/^> ?/, ""));
    out.push(":::");
  }
  return out;
}

/** Plain text of a Markdown inline string (for titles and descriptions). */
const plain = (s: string) =>
  s
    .replace(/!\[[^\]]*\]\([^)]*\)/g, "")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/<[^>]+>/g, "")
    .replace(/[`*_]/g, "")
    .replace(/\s+/g, " ")
    .trim();

/** The first paragraph of prose, as a meta description of at most ~200 characters. */
function describe(lines: string[]): string | undefined {
  let para: string[] = [];
  let inFence = false;
  for (const line of lines) {
    if (fence.test(line)) {
      inFence = !inFence;
      if (para.length) break;
      continue;
    }
    if (inFence) continue;
    if (line.trim() === "") {
      if (para.length) break;
      continue;
    }
    if (para.length === 0 && /^\s*([#>|<:]|[-*+] |\d+\. |!\[|Status:|- Status)/.test(line)) continue;
    para.push(line);
  }
  let d = plain(para.join(" "));
  if (!d) return undefined;
  if (d.length > 200) d = `${d.slice(0, 200).replace(/\s+\S*$/, "")}…`;
  return d;
}

const yaml = (v: string) => JSON.stringify(v);

function frontmatter(fields: Record<string, string | boolean | undefined>, extra = ""): string {
  const body = Object.entries(fields)
    .filter(([, v]) => v !== undefined)
    .map(([k, v]) => `${k}: ${typeof v === "string" ? yaml(v) : v}`)
    .join("\n");
  return `---\n${body}${extra}\n---\n`;
}

// ---- pages ----------------------------------------------------------------------------------

const internalBanner = `\nbanner:\n  content: ${yaml(
  "Contributor documentation: how the project is built and maintained. Not needed to run xtandard-image.",
)}`;

function fromRepo(p: Page): string {
  const src = p.src!;
  let lines = readFileSync(join(repoRoot, src), "utf8").replace(/\r\n/g, "\n").split("\n");
  let title: string;
  let sectionOf: string | undefined;
  if (p.section) {
    const start = lines.findIndex((l) => l === `## ${p.section}`);
    if (start < 0) {
      fail(`${src}: no "## ${p.section}" section for /${p.slug}/`);
      return "";
    }
    let end = lines.findIndex((l, i) => i > start && /^## /.test(l));
    if (end < 0) end = lines.length;
    lines = lines.slice(start + 1, end);
    title = p.title ?? p.section;
    sectionOf = src;
  } else {
    let h1 = -1;
    let open = false;
    for (let i = 0; i < lines.length; i++) {
      if (fence.test(lines[i]!)) open = !open;
      if (!open && /^# \S/.test(lines[i]!)) {
        h1 = i;
        break;
      }
    }
    if (h1 < 0) {
      fail(`${src}: no H1 to take the title from`);
      return "";
    }
    // Only a logo (an image or an HTML block) may come before the H1; it is dropped.
    const pre = lines.slice(0, h1).filter((l) => l.trim() !== "");
    if (pre.some((l) => !/^\s*(<|!\[)/.test(l))) fail(`${src}: unexpected text before the H1`);
    title = plain(lines[h1]!.slice(2));
    lines = lines.slice(h1 + 1);
  }
  const description = describe(lines);
  const body = mapProse(alerts(lines), (l) => rewriteLine(l, src, sectionOf)).join("\n");
  return (
    frontmatter(
      {
        title,
        description,
        editUrl: `${repoUrl}/edit/${branch}/${src}`,
        pagefind: p.internal ? false : undefined,
      },
      p.internal ? internalBanner : "",
    ) + body.replace(/^\n+/, "\n")
  );
}

async function fromSite(p: Page): Promise<string> {
  let text = await Bun.file(join(siteDir, "content", p.site!)).text();
  text = text.replaceAll("@MAJOR@", major).replaceAll("@IMAGE@", image).replaceAll("@BASE@", siteBase);
  if (text.includes("@ADR_LIST@")) {
    const list = all
      .filter((q) => q.slug.startsWith("project/adr/"))
      .map((q) => {
        const h1 = /^# (.+)$/m.exec(readFileSync(join(repoRoot, q.src!), "utf8"))![1]!;
        return `- [${plain(h1)}](${route(q.slug)})`;
      })
      .join("\n");
    text = text.replace("@ADR_LIST@", list);
  }
  if (!/^---\n[\s\S]*?\neditUrl:/.test(text)) {
    text = text.replace(/^---\n/, `---\neditUrl: ${yaml(`${repoUrl}/edit/${branch}/site/content/${p.site}`)}\n`);
  }
  const left = /@[A-Z_]+@/.exec(text.replace(/```[\s\S]*?```/g, ""));
  if (left) fail(`site/content/${p.site}: unreplaced ${left[0]}`);
  return text;
}

rmSync(outDir, { recursive: true, force: true });
let written = 0;
for (const p of all) {
  const text = p.site ? await fromSite(p) : fromRepo(p);
  if (!text) continue;
  const ext = p.site?.endsWith(".mdx") ? "mdx" : "md";
  const file = join(outDir, p.slug === "" ? `index.${ext}` : `${p.slug}.${ext}`);
  mkdirSync(dirname(file), { recursive: true });
  await Bun.write(file, text);
  written++;
}

// Every Markdown file the docs link to that is not a page stays on GitHub; list the ones under
// docs/ so a new doc is not forgotten in the manifest.
const covered = new Set(all.map((p) => p.src).filter(Boolean));
const docsMd = new Bun.Glob("docs/**/*.md").scanSync({ cwd: repoRoot });
const missing = [...docsMd].filter((f) => !covered.has(normalize(f)) && !f.startsWith("docs/schemastore/"));
for (const f of missing) fail(`${f} is not in site/manifest.ts (add it to the sidebar)`);

if (errors.length > 0) {
  for (const e of errors) console.error(`sync: ${e}`);
  console.error(`sync: ${errors.length} error(s)`);
  process.exit(1);
}
console.log(`sync: ${written} pages in ${outDir.slice(repoRoot.length)} (schema ${major}, base ${siteBase})`);
