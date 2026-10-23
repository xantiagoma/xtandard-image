// Checks the licence of every npm package installed from bun.lock against
// the allowlist of ADR 0006 (MIT/BSD/Apache/ISC), as ADR 0009 decision 3
// requires. Run after `bun install --frozen-lockfile`:
//
//   bun scripts/check-ts-licenses.ts
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

const root = new URL("../", import.meta.url).pathname;

function ok(expr: string): boolean {
  const e = expr.trim().replace(/^\((.*)\)$/, "$1");
  if (/ OR /i.test(e)) return e.split(/ OR /i).some(ok);
  if (/ AND /i.test(e)) return e.split(/ AND /i).every(ok);
  return allowed.has(e);
}

type Manifest = { name?: string; version?: string; license?: string | { type?: string }; private?: boolean };

const seen = new Map<string, string>();
// Bun's isolated store (node_modules/.bun/<pkg>/node_modules/<name>) and the
// hoisted layout (node_modules/<name>).
const patterns = [
  "node_modules/.bun/*/node_modules/*/package.json",
  "node_modules/.bun/*/node_modules/@*/*/package.json",
  "node_modules/*/package.json",
  "node_modules/@*/*/package.json",
];
for (const pattern of patterns) {
  for await (const file of new Bun.Glob(pattern).scan({ cwd: root, followSymlinks: false, onlyFiles: true })) {
    const m = (await Bun.file(`${root}${file}`).json()) as Manifest;
    if (!m.name || !m.version || m.private) continue;
    const license = typeof m.license === "string" ? m.license : (m.license?.type ?? "");
    seen.set(`${m.name}@${m.version}`, license);
  }
}

// Published workspace packages ship no runtime dependencies.
let runtimeDeps = 0;
const workspaceManifests: string[] = [];
for (const pattern of ["clients/typescript/package.json", "packages/*/package.json"]) {
  for await (const file of new Bun.Glob(pattern).scan({ cwd: root })) workspaceManifests.push(file);
}
if (!workspaceManifests.includes("clients/typescript/package.json")) {
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
  console.error("no installed packages found: run `bun install --frozen-lockfile` first");
  process.exit(1);
}
const bad = [...seen].filter(([pkg, l]) => !ok(l) && !devOnlyExceptions.has(pkg)).sort();
for (const [pkg, l] of bad) console.error(`${pkg}: licence ${JSON.stringify(l)} is not in the allowlist`);
const excepted = [...seen.keys()].filter((pkg) => devOnlyExceptions.has(pkg));
console.log(`${seen.size} npm packages checked, ${bad.length} not allowed, ${excepted.length} dev-only exception(s)`);
if (bad.length > 0 || runtimeDeps > 0) process.exit(1);
