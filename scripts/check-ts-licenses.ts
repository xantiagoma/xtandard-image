// Checks the licence of every npm package installed from bun.lock against
// the allowlist of ADR 0006 (MIT/BSD/Apache/ISC), as ADR 0009 decision 3
// requires. Run after `bun install --frozen-lockfile`:
//
//   bun scripts/check-ts-licenses.ts         the root workspace (bun.lock)
//   bun scripts/check-ts-licenses.ts site    a separate Bun project with its own
//                                            bun.lock: the documentation site
//                                            (ADR 0015; `bun run licenses` in site/)
//
// An SPDX expression passes when every AND term, and at least one side of
// every OR, is allowed. Published packages must also have no runtime
// "dependencies" (ADR 0009 decision 3), so everything checked here is
// development tooling.

const allowed = new Set([
  "MIT",
  "MIT-0",
  "ISC",
  "0BSD",
  "BSD-2-Clause",
  "BSD-3-Clause",
  "Apache-2.0",
]);

// Development tools that are only run, never shipped (ADR 0006: "external
// tools that are only run … may carry any licence"), with a permissive
// licence outside the list. Each entry names its path into the tree.
const devOnlyExceptions = new Map([
  [
    "argparse@2.0.1",
    "Python-2.0 (PSF, permissive): openapi-typescript → @redocly/openapi-core → js-yaml; runs in `bun run generate` only",
  ],
]);

// The same, matched by package name for every version: the prebuilt libvips
// of sharp (an optional dependency of Astro) is one package per OS and CPU.
// LGPL-3.0-or-later and dynamically loaded by sharp, which ADR 0006 allows
// even in shipped artefacts; here it only runs during the documentation
// site's build, and nothing of it is published (ADR 0015).
const devOnlyNameExceptions: [RegExp, string][] = [
  [/^@img\/sharp-libvips-[a-z0-9-]+$/, "LGPL-3.0-or-later, libvips loaded by sharp; documentation site build only (ADR 0015)"],
];
const nameOf = (pkg: string) => pkg.slice(0, pkg.lastIndexOf("@"));
const excepted = (pkg: string) => devOnlyExceptions.has(pkg) || devOnlyNameExceptions.some(([re]) => re.test(nameOf(pkg)));

const repoRoot = new URL("../", import.meta.url).pathname;
// A project directory (relative to the repository root) checks that
// project's node_modules instead of the root workspace's.
const project = process.argv[2]?.replace(/\/+$/, "");
const root = project ? `${repoRoot}${project}/` : repoRoot;

// The site's build tools only run, and the site they produce is HTML, CSS
// and JS written by Astro (MIT) and Starlight (MIT), so the permissive
// licences that deploy/check/licenses.ts accepts for its run-only tools are
// accepted there too (ADR 0015): BlueOak-1.0.0 (lru-cache, sax,
// common-ancestor-path), CC0-1.0 (mdn-data, data only) and MPL-2.0 (lightningcss, the
// CSS minifier; file-level copyleft, which ADR 0006 allows even when shipped).
if (project) for (const l of ["BlueOak-1.0.0", "CC0-1.0", "MPL-2.0"]) allowed.add(l);

function ok(expr: string): boolean {
  const e = expr.trim().replace(/^\((.*)\)$/, "$1");
  if (/ OR /i.test(e)) return e.split(/ OR /i).some(ok);
  if (/ AND /i.test(e)) return e.split(/ AND /i).every(ok);
  return allowed.has(e);
}

type Manifest = { name?: string; version?: string; license?: string | { type?: string }; private?: boolean };

const seen = new Map<string, string>();
// Bun's isolated store (node_modules/.bun/<pkg>/node_modules/<name>) and the
// hoisted layout (node_modules/<name>). A separate project is hoisted, where
// packages also nest (node_modules/a/node_modules/b): scan every depth there,
// keeping package roots only.
const patterns = project
  ? ["node_modules/**/package.json"]
  : [
      "node_modules/.bun/*/node_modules/*/package.json",
      "node_modules/.bun/*/node_modules/@*/*/package.json",
      "node_modules/*/package.json",
      "node_modules/@*/*/package.json",
    ];
const packageRoot = /(^|\/)node_modules\/(@[^/]+\/)?[^/@.][^/]*\/package\.json$/;
for (const pattern of patterns) {
  for await (const file of new Bun.Glob(pattern).scan({ cwd: root, followSymlinks: false, onlyFiles: true, dot: !!project })) {
    if (project && !packageRoot.test(file)) continue;
    const m = (await Bun.file(`${root}${file}`).json()) as Manifest;
    if (!m.name || !m.version || m.private) continue;
    const license = typeof m.license === "string" ? m.license : (m.license?.type ?? "");
    seen.set(`${m.name}@${m.version}`, license);
  }
}

// Published workspace packages ship no runtime dependencies. A separate
// project (the site) is private and publishes nothing.
let runtimeDeps = 0;
const workspaceManifests: string[] = [];
for (const pattern of project ? [] : ["clients/typescript/package.json", "packages/*/package.json"]) {
  for await (const file of new Bun.Glob(pattern).scan({ cwd: root })) workspaceManifests.push(file);
}
if (!project && !workspaceManifests.includes("clients/typescript/package.json")) {
  console.error("clients/typescript/package.json not found");
  process.exit(1);
}
for (const file of workspaceManifests) {
  const m = (await Bun.file(`${root}${file}`).json()) as Manifest & { dependencies?: Record<string, string> };
  if (m.private) continue;
  for (const dep of Object.keys(m.dependencies ?? {})) {
    console.error(`${m.name}: runtime dependency ${dep} needs an ADR (ADR 0009 decision 3)`);
    runtimeDeps++;
  }
}

if (seen.size === 0) {
  console.error(`no installed packages found in ${root}: run \`bun install --frozen-lockfile\` first`);
  process.exit(1);
}
const bad = [...seen].filter(([pkg, l]) => !ok(l) && !excepted(pkg)).sort();
for (const [pkg, l] of bad) console.error(`${pkg}: licence ${JSON.stringify(l)} is not in the allowlist`);
const exceptions = [...seen.keys()].filter(excepted);
console.log(`${seen.size} npm packages checked, ${bad.length} not allowed, ${exceptions.length} dev-only exception(s)`);
if (bad.length > 0 || runtimeDeps > 0) process.exit(1);
