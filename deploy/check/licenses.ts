// Licence check of the deploy/ Node tooling (deploy/pulumi, deploy/sst, deploy/cdk) against the
// ADR 0006 allowlist, with the rules of scripts/check-ts-licenses.ts: an SPDX expression passes
// when every AND term, and at least one side of every OR, is allowed. Everything here is
// development tooling that is only run to describe a deployment; nothing ships in an image or
// a client (ADR 0006: external tools that are only run may carry any licence, and each
// exception below is named with its reason).
//
//   bun deploy/check/licenses.ts        (after bun install in each directory)

const allowed = new Set(["MIT", "MIT-0", "ISC", "0BSD", "BSD-2-Clause", "BSD-3-Clause", "Apache-2.0"]);

// Permissive licences outside the list, accepted for these run-only tools (Deviation in WP-149,
// docs/DEPLOY.md "Licences"): BlueOak-1.0.0 (isaacs' glob, minimatch, tar, lru-cache…),
// CC0-1.0 (spdx-license-ids, data) and Python-2.0 (argparse, as in scripts/check-ts-licenses.ts).
for (const l of ["BlueOak-1.0.0", "CC0-1.0", "Python-2.0"]) allowed.add(l);

const devOnlyExceptions = new Map([
  ["spdx-exceptions@2.5.0", "CC-BY-3.0: the SPDX licence-exception list (data), pulled by Pulumi's npm tooling"],
  ["@logdna/tail-file@2.2.0", "'SEE LICENSE IN LICENSE': the LICENSE file is MIT (checked); @pulumi/pulumi dependency"],
]);

function ok(expr: string): boolean {
  const e = expr.trim().replace(/^\((.*)\)$/, "$1");
  if (/ OR /i.test(e)) return e.split(/ OR /i).some(ok);
  if (/ AND /i.test(e)) return e.split(/ AND /i).every(ok);
  return allowed.has(e);
}

type Manifest = { name?: string; version?: string; license?: string | { type?: string }; private?: boolean };

const deployDir = new URL("../", import.meta.url).pathname;
let failed = false;
for (const project of ["pulumi", "sst", "cdk"]) {
  const root = `${deployDir}${project}/`;
  const seen = new Map<string, string>();
  for (const pattern of ["node_modules/**/package.json"]) {
    for await (const file of new Bun.Glob(pattern).scan({ cwd: root, followSymlinks: false, onlyFiles: true })) {
      // Only package roots: <...>/node_modules/<name>/package.json or <...>/node_modules/@s/<name>/package.json.
      if (!/(^|\/)node_modules\/(@[^/]+\/)?[^/@][^/]*\/package\.json$/.test(file)) continue;
      const m = (await Bun.file(`${root}${file}`).json()) as Manifest;
      if (!m.name || !m.version || m.private) continue;
      const license = typeof m.license === "string" ? m.license : (m.license?.type ?? "");
      seen.set(`${m.name}@${m.version}`, license);
    }
  }
  const bad = [...seen].filter(([id, l]) => !ok(l) && !devOnlyExceptions.has(id));
  for (const [id, l] of bad) console.log(`     ${id}: ${l || "(none)"}`);
  const exc = [...seen.keys()].filter((id) => devOnlyExceptions.has(id));
  for (const id of exc) console.log(`     exception ${id}: ${devOnlyExceptions.get(id)}`);
  console.log(`${bad.length ? "FAIL" : "ok  "} deploy/${project} licences (${seen.size} packages)`);
  failed ||= bad.length > 0;
}
process.exit(failed ? 1 : 0);
