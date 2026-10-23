// The official clients' code generator (ADR 0013): reads the
// language-neutral spec (clients/spec/spec.json, written by internal/apidoc)
// and runs every emitter in clients/codegen/emitters/, each of which
// returns the generated files of one client (typed option tables, response
// types, constants). Emitters are discovered by file name, so a new
// language adds one file and no line here. An emitter is a refiner: it
// builds the client's files as a code model (model.ts) from the neutral
// model (build.ts) and prints them with its language's printer
// (lang/<lang>.ts) on the Doc algebra (doc.ts); see model.ts.
//
//   bun clients/codegen/generate.ts                  write every client's files
//   bun clients/codegen/generate.ts --check          exit 1 if a committed file is stale
//   bun clients/codegen/generate.ts --only python    one emitter
//
// task clients:generate / task clients:check wrap it.

import type { Emitter, Spec } from "./spec.ts";

const root = new URL("../../", import.meta.url);
const args = process.argv.slice(2);
const check = args.includes("--check");
const onlyAt = args.indexOf("--only");
const only = onlyAt >= 0 ? args[onlyAt + 1] : undefined;

const spec = (await Bun.file(new URL("clients/spec/spec.json", root)).json()) as Spec;
if (spec.spec_version !== 1) {
  console.error(`clients/spec/spec.json has spec_version ${spec.spec_version}; this generator reads version 1`);
  process.exit(1);
}

const emitterDir = new URL("emitters/", import.meta.url);
const glob = new Bun.Glob("*.ts");
const files = [...glob.scanSync({ cwd: emitterDir.pathname })].filter((f) => !f.endsWith(".test.ts")).sort();
let emitters: Emitter[] = [];
for (const f of files) {
  const mod = (await import(new URL(f, emitterDir).href)) as { emitter?: Emitter };
  if (!mod.emitter) {
    console.error(`clients/codegen/emitters/${f} exports no \`emitter\``);
    process.exit(1);
  }
  emitters.push(mod.emitter);
}
if (only !== undefined) {
  emitters = emitters.filter((e) => e.language === only);
  if (emitters.length === 0) {
    console.error(`no emitter for ${only}`);
    process.exit(1);
  }
}

let stale = 0;
for (const e of emitters) {
  const out = e.emit(spec);
  for (const [path, content] of Object.entries(out)) {
    if (!path.startsWith(`clients/${e.language}/`)) {
      console.error(`emitter ${e.language} writes ${path}, outside clients/${e.language}/`);
      process.exit(1);
    }
    const target = Bun.file(new URL(path, root));
    if (check) {
      const current = (await target.exists()) ? await target.text() : "";
      if (current !== content) {
        console.error(`${path} is stale: run \`task clients:generate\` and commit the result`);
        stale++;
      }
      continue;
    }
    await Bun.write(target, content);
    console.log(`wrote ${path}`);
  }
}
if (stale > 0) process.exit(1);
if (check) console.log(`generated files are current (${emitters.map((e) => e.language).join(", ")})`);
