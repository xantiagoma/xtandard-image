// Post-build check of the documentation site (ADR 0015), run by `bun run build`:
//
//   1. every local href/src/srcset in every HTML file of dist/pages/ resolves to a built file,
//      and every #fragment to an id on the target page (this covers what
//      starlight-links-validator does not see: the sidebar, the home page components, and
//      links into the static files);
//   2. the static files scripts/pages-build.sh assembled (schemas, Scalar page, assets) are in
//      dist/pages/ byte for byte, so the schema $id URLs and /api/ serve exactly what they did;
//   3. the files GitHub Pages needs are there (.nojekyll, 404.html).
//
//   bun scripts/check-dist.ts
import { existsSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { repoRoot, siteBase } from "../manifest.ts";

const dist = join(repoRoot, "dist/pages");
const staticDir = join(repoRoot, "dist/pages-static");
const errors: string[] = [];

const htmlFiles = [...new Bun.Glob("**/*.html").scanSync({ cwd: dist, dot: true })].sort();
const ids = new Map<string, Set<string>>();
const idsOf = (file: string) => {
  let set = ids.get(file);
  if (!set) {
    const html = readFileSync(join(dist, file), "utf8");
    set = new Set([...html.matchAll(/\sid="([^"]+)"/g)].map((m) => m[1]!));
    ids.set(file, set);
  }
  return set;
};

/** Built file for a local URL path (without the base), or undefined. */
function resolve(path: string): string | undefined {
  const candidates = path.endsWith("/") || path === "" ? [`${path}index.html`] : [path, `${path}/index.html`];
  return candidates.find((c) => existsSync(join(dist, c)) && statSync(join(dist, c)).isFile());
}

let checked = 0;
for (const file of htmlFiles) {
  const html = readFileSync(join(dist, file), "utf8");
  const dir = file.includes("/") ? file.slice(0, file.lastIndexOf("/") + 1) : "";
  const urls: string[] = [];
  for (const m of html.matchAll(/\s(?:href|src)="([^"]*)"/g)) urls.push(m[1]!);
  for (const m of html.matchAll(/\ssrcset="([^"]*)"/g)) for (const c of m[1]!.split(",")) urls.push(c.trim().split(/\s+/)[0]!);
  for (const raw of urls) {
    const url = raw.replaceAll("&amp;", "&");
    if (/^[a-z][a-z0-9+.-]*:/i.test(url) || url.startsWith("//") || url === "") continue;
    const [pathAndQuery, hash] = url.split("#", 2) as [string, string | undefined];
    const pathOnly = pathAndQuery.split("?")[0]!;
    let path: string;
    if (pathOnly === "") path = file;
    else if (pathOnly.startsWith("/")) {
      if (pathOnly !== siteBase && !pathOnly.startsWith(`${siteBase}/`)) {
        errors.push(`${file}: ${url} is outside the base ${siteBase}`);
        continue;
      }
      path = decodeURIComponent(pathOnly.slice(siteBase.length + 1));
    } else path = new URL(decodeURIComponent(pathOnly), `http://x/${dir}`).pathname.slice(1);
    const target = pathOnly === "" ? file : resolve(path);
    checked++;
    if (!target) {
      errors.push(`${file}: ${url} does not exist`);
      continue;
    }
    if (hash && target.endsWith(".html") && !idsOf(target).has(decodeURIComponent(hash))) {
      errors.push(`${file}: ${url}: no id "${hash}" in ${target}`);
    }
  }
}

// The static files, byte for byte.
const statics = [...new Bun.Glob("**/*").scanSync({ cwd: staticDir, dot: true })].sort();
for (const f of statics) {
  const out = join(dist, f);
  if (!existsSync(out)) errors.push(`${f} from pages-build.sh is missing in dist/pages`);
  else if (!readFileSync(out).equals(readFileSync(join(staticDir, f)))) errors.push(`${f} differs from pages-build.sh's copy`);
}
for (const f of [".nojekyll", "404.html", "api/index.html", "schema/latest/config.schema.json", "schema/latest/openapi.json"]) {
  if (!existsSync(join(dist, f))) errors.push(`${f} is missing`);
}

if (errors.length > 0) {
  for (const e of errors) console.error(`check-dist: ${e}`);
  console.error(`check-dist: ${errors.length} error(s)`);
  process.exit(1);
}
console.log(`check-dist: ${htmlFiles.length} HTML files, ${checked} local links resolved, ${statics.length} static files identical`);
