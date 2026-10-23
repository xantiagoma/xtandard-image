// Checks the playground's code snippets (internal/playground/assets/
// snippets/, WP-145) by running them: a set of builder states is turned
// into each language's snippet, the snippet is written into a scratch
// project that depends on the local client (path dependency), it runs with
// that client's toolchain (the one its mise.toml pins; Swift from the PATH),
// with gofmt/go vet for Go and dart analyze for Dart, and the URL it prints
// must equal the URL the TypeScript client
// (the reference implementation, clients/typescript) builds for the same
// model. Fixed test keys (clients/spec/conformance.json) and a fixed IV
// make signed and enc/ URLs reproducible.
//
// It also compares the playground's own URL (grammar.js build(), the URL
// the page shows) with the reference written with canonical names, after
// normalising what may legitimately differ (order of query keys,
// percent-encoding, where the stream options sit).
//
//   bun scripts/playground-snippets-check.ts            # every language
//   ONLY=python,go bun scripts/playground-snippets-check.ts
//   task playground:snippets
//
// Scratch projects live in $XI_SNIPPETS_DIR (default: <tmp>/xi-snippets)
// and are reused between runs. A language whose toolchain is missing is
// reported "unavailable", never as a pass. Slow (compilers, Gradle):
// it is not part of `go test` or CI; run it after changing snippets.js or a
// client's API.
import { $ } from "bun";
import { existsSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { BASE, CASES, CLIENTS, ENV, normalise, reference, ROOT } from "./playground-snippets-cases.ts";
// @ts-expect-error: plain ES module without types
import { LANGUAGES, QUERY_LANGUAGES, renderSnippet } from "../internal/playground/assets/snippets/index.js";

const WORK = process.env.XI_SNIPPETS_DIR ?? join(tmpdir(), "xi-snippets");
const ONLY = (process.env.ONLY ?? "").split(",").map((s) => s.trim()).filter(Boolean);
const VERBOSE = process.env.VERBOSE === "1";

// ---- languages ----------------------------------------------------------------

type Lang = { setup: (dir: string) => Promise<void>; run: (dir: string, code: string) => Promise<string>; tool: () => Promise<boolean> };
const has = async (...cmd: string[]) => (await $`${cmd}`.quiet().nothrow()).exitCode === 0;
// toolEnv is the environment mise sets up for a client directory (its
// pinned toolchain), applied to commands that run in the scratch project.
const envs = new Map<string, Record<string, string>>();
async function toolEnv(client: string): Promise<Record<string, string>> {
  if (!envs.has(client)) {
    const r = await $`mise env -C ${join(CLIENTS, client)} --json`.quiet().nothrow();
    envs.set(client, r.exitCode === 0 ? JSON.parse(r.stdout.toString()) : {});
  }
  return envs.get(client)!;
}
const msh = async (client: string, cmd: string[], cwd: string) => sh(cmd, cwd, await toolEnv(client));
const mhas = async (client: string, ...cmd: string[]) => {
  const env = { ...process.env, ...(await toolEnv(client)) } as Record<string, string>;
  const bin = Bun.which(cmd[0]!, { PATH: env.PATH ?? "" });
  if (!bin) return false;
  const p = Bun.spawn([bin, ...cmd.slice(1)], { env, stdout: "ignore", stderr: "ignore" });
  return (await p.exited) === 0;
};
async function sh(cmd: string[], cwd: string, env: Record<string, string> = {}): Promise<string> {
  const full = { ...process.env, ...env, ...ENV } as Record<string, string>;
  const bin = Bun.which(cmd[0]!, { PATH: full.PATH ?? "" }) ?? cmd[0]!;
  const p = Bun.spawn([bin, ...cmd.slice(1)], { cwd, env: full, stdout: "pipe", stderr: "pipe" });
  const [out, err] = await Promise.all([new Response(p.stdout).text(), new Response(p.stderr).text()]);
  if ((await p.exited) !== 0) throw new Error(`${cmd.join(" ")} failed:\n${err || out}`.slice(0, 4000));
  return out;
}
const lastLine = (s: string) => s.trim().split("\n").at(-1)?.trim() ?? "";
const write = (path: string, text: string) => { mkdirSync(join(path, ".."), { recursive: true }); writeFileSync(path, text.endsWith("\n") ? text : `${text}\n`); };

const LANGS: Record<string, Lang> = {
  typescript: {
    tool: async () => true,
    setup: async (dir) => {
      const pkg = join(dir, "node_modules/@xtandard/image");
      mkdirSync(pkg, { recursive: true });
      write(join(pkg, "package.json"), JSON.stringify({ name: "@xtandard/image", type: "module", main: "index.ts" }));
      write(join(pkg, "index.ts"), `export * from ${JSON.stringify(join(CLIENTS, "typescript/src/index.ts"))};\n`);
    },
    run: async (dir, code) => { write(join(dir, "main.ts"), code); return lastLine(await sh(["bun", "main.ts"], dir)); },
  },
  python: {
    tool: () => mhas("python", "uv", "--version"),
    setup: async (dir) => {
      await msh("python", ["uv", "venv", "-q", "--allow-existing", join(dir, ".venv")], dir);
      await msh("python", ["uv", "pip", "install", "-q", "--python", join(dir, ".venv/bin/python"), join(CLIENTS, "python")], dir);
    },
    run: async (dir, code) => { write(join(dir, "main.py"), code); return lastLine(await sh([join(dir, ".venv/bin/python"), "-I", "main.py"], dir)); },
  },
  go: {
    tool: () => mhas("go", "go", "version"),
    setup: async (dir) => {
      write(join(dir, "go.mod"), `module snippet\n\ngo 1.26.0\n\nrequire github.com/xantiagoma/xtandard-image/clients/go v0.0.0\n\nreplace github.com/xantiagoma/xtandard-image/clients/go => ${join(CLIENTS, "go")}\n`);
    },
    run: async (dir, code) => {
      write(join(dir, "main.go"), code);
      await msh("go", ["gofmt", "-d", "main.go"], dir).then((o) => { if (o.trim()) throw new Error(`gofmt would reformat:\n${o}`); });
      await msh("go", ["go", "vet", "."], dir);
      return lastLine(await msh("go", ["go", "run", "."], dir));
    },
  },
  php: {
    tool: () => mhas("php", "composer", "--version"),
    setup: async (dir) => {
      write(join(dir, "composer.json"), JSON.stringify({
        repositories: [{ type: "path", url: join(CLIENTS, "php"), options: { symlink: true } }],
        require: { "xtandard/image": "*@dev" },
        "minimum-stability": "dev",
      }, null, 2));
      await msh("php", ["composer", "update", "-q", "--no-interaction"], dir);
    },
    run: async (dir, code) => { write(join(dir, "main.php"), code); return lastLine(await msh("php", ["php", "main.php"], dir)); },
  },
  ruby: {
    tool: () => mhas("ruby", "ruby", "--version"),
    setup: async () => {},
    run: async (dir, code) => { write(join(dir, "main.rb"), code); return lastLine(await msh("ruby", ["ruby", "-I", join(CLIENTS, "ruby/lib"), "main.rb"], dir)); },
  },
  rust: {
    tool: () => mhas("rust", "cargo", "--version"),
    setup: async (dir) => {
      write(join(dir, "Cargo.toml"), `[package]\nname = "snippet"\nversion = "0.0.0"\nedition = "2024"\n\n[dependencies]\nxtandard-image = { path = ${JSON.stringify(join(CLIENTS, "rust"))}, default-features = false }\n`);
      write(join(dir, "src/main.rs"), "fn main() {}\n");
      await msh("rust", ["cargo", "build", "-q"], dir);
    },
    run: async (dir, code) => { write(join(dir, "src/main.rs"), code); return lastLine(await msh("rust", ["cargo", "run", "-q"], dir)); },
  },
  java: {
    tool: () => mhas("java", "mvn", "--version"),
    setup: async (dir) => {
      // The client's own build (target/ is git-ignored), then its jar is copied.
      await msh("java", ["mvn", "-q", "-B", "-f", join(CLIENTS, "java/pom.xml"), "package", "-DskipTests", "-Dmaven.javadoc.skip=true", "-Dmaven.source.skip=true", "-Dgpg.skip=true"], dir);
      await sh(["sh", "-c", `cp "$(ls ${join(CLIENTS, "java/target")}/xtandard-image-*.jar | grep -v -e sources -e javadoc | head -1)" client.jar`], dir);
    },
    run: async (dir, code) => {
      write(join(dir, "Main.java"), code);
      return lastLine(await msh("java", ["java", "-cp", "client.jar", "Main.java"], dir));
    },
  },
  kotlin: {
    tool: () => mhas("kotlin", "gradle", "--version"),
    setup: async (dir) => {
      write(join(dir, "settings.gradle.kts"), `rootProject.name = "snippet"\nincludeBuild(${JSON.stringify(join(CLIENTS, "kotlin"))})\n`);
      // The client's own Kotlin version (its metadata must be readable).
      const kv = /^kotlin\s*=\s*"([^"]+)"/m.exec(await Bun.file(join(CLIENTS, "kotlin/gradle/libs.versions.toml")).text())?.[1] ?? "2.4.21";
      write(join(dir, "build.gradle.kts"), `plugins {\n    kotlin("jvm") version "${kv}"\n    application\n}\nrepositories { mavenCentral() }\ndependencies { implementation("io.github.xantiagoma:xtandard-image-kmp") }\nkotlin { jvmToolchain(21) }\napplication { mainClass.set("MainKt") }\n`);
      write(join(dir, "src/main/kotlin/Main.kt"), "fun main() {}\n");
      await msh("kotlin", ["gradle", "-q", "--console=plain", "classes"], dir);
    },
    run: async (dir, code) => { write(join(dir, "src/main/kotlin/Main.kt"), code); return lastLine(await msh("kotlin", ["gradle", "-q", "--console=plain", "run"], dir)); },
  },
  csharp: {
    tool: () => mhas("dotnet", "dotnet", "--version"),
    setup: async (dir) => {
      write(join(dir, "Snippet.csproj"), `<Project Sdk="Microsoft.NET.Sdk">\n  <PropertyGroup>\n    <OutputType>Exe</OutputType>\n    <TargetFramework>net10.0</TargetFramework>\n    <ImplicitUsings>enable</ImplicitUsings>\n    <Nullable>enable</Nullable>\n    <TreatWarningsAsErrors>true</TreatWarningsAsErrors>\n  </PropertyGroup>\n  <ItemGroup>\n    <ProjectReference Include=${JSON.stringify(join(CLIENTS, "dotnet/src/Xtandard.Image/Xtandard.Image.csproj"))} />\n  </ItemGroup>\n</Project>\n`);
      write(join(dir, "Program.cs"), "System.Console.WriteLine();\n");
      await msh("dotnet", ["dotnet", "build", "-v", "q", "--nologo"], dir);
    },
    run: async (dir, code) => { write(join(dir, "Program.cs"), code); return lastLine(await msh("dotnet", ["dotnet", "run", "-v", "q", "--nologo"], dir)); },
  },
  elixir: {
    tool: () => mhas("elixir", "mix", "--version"),
    setup: async (dir) => {
      write(join(dir, "mix.exs"), `defmodule Snippet.MixProject do\n  use Mix.Project\n  def project, do: [app: :snippet, version: "0.0.0", deps: [{:xtandard_image, path: ${JSON.stringify(join(CLIENTS, "elixir"))}}]]\nend\n`);
      await msh("elixir", ["mix", "deps.get"], dir);
      await msh("elixir", ["mix", "compile"], dir);
    },
    run: async (dir, code) => { write(join(dir, "main.exs"), code); return lastLine(await msh("elixir", ["mix", "run", "main.exs"], dir)); },
  },
  swift: {
    // Swift 6.2+ on the PATH (Xcode on macOS; on Linux, run this inside the client's swift image).
    tool: () => has("swift", "--version"),
    setup: async (dir) => {
      write(join(dir, "Package.swift"), `// swift-tools-version:6.2\nimport PackageDescription\nlet package = Package(\n  name: "snippet",\n  platforms: [.macOS(.v13)],\n  dependencies: [.package(path: ${JSON.stringify(ROOT)})],\n  targets: [.executableTarget(name: "snippet", dependencies: [.product(name: "XtandardImage", package: "${ROOT.split("/").at(-1)}")])]\n)\n`);
      write(join(dir, "Sources/snippet/main.swift"), "print(\"\")\n");
      await sh(["swift", "build", "-q"], dir);
    },
    run: async (dir, code) => { write(join(dir, "Sources/snippet/main.swift"), code); return lastLine(await sh(["swift", "run", "-q", "snippet"], dir)); },
  },
  dart: {
    tool: () => mhas("dart", "dart", "--version"),
    setup: async (dir) => {
      write(join(dir, "pubspec.yaml"), `name: snippet\nenvironment:\n  sdk: ^3.5.0\ndependencies:\n  xtandard_image:\n    path: ${join(CLIENTS, "dart")}\n`);
      await msh("dart", ["dart", "pub", "get"], dir);
    },
    run: async (dir, code) => {
      write(join(dir, "bin/main.dart"), code);
      await msh("dart", ["dart", "analyze", "--fatal-infos", "bin/main.dart"], dir);
      return lastLine(await msh("dart", ["dart", "run", "bin/main.dart"], dir));
    },
  },
};

// ---- run ------------------------------------------------------------------------

const models = await Promise.all(CASES.map(async (c) => {
  const model = c.model();
  // An HTTP QUERY case prints the request in the languages that lower it
  // (QUERY_LANGUAGES) and the GET URL in the others.
  return { c, model, expect: await reference(model), get: await reference({ ...model, queryMethod: false }) };
}));

console.log(`Cases (${models.length}):`);
for (const { c, model, expect } of models) {
  let pg = "";
  const p = model._playground;
  if (p && !model.encryption && !model.pdf && model.kind !== "xisig") {
    const unsigned = await reference({ ...model, signed: false, queryMethod: false }, "canonical", true);
    const page = `${BASE}${p.m.prefix.replace(/\/+$/, "")}${p.req.path.startsWith("/video") && p.m.grammar === "imgproxy" ? "/video/insecure" + p.req.path.slice(6) : (p.m.grammar === "imgproxy" ? "/insecure" : "") + p.req.path}${p.req.query ? `?${p.req.query}` : ""}`;
    pg = normalise(page) === normalise(unsigned) ? (page === unsigned ? "page URL identical" : "page URL equivalent") : `PAGE URL DIFFERS:\n      page ${page}\n      ref  ${unsigned}`;
  }
  console.log(`  ${c.name.padEnd(14)} ${model.kind.padEnd(9)} ${c.what}${pg ? ` · ${pg}` : ""}\n    ${expect}`);
}

const langs = LANGUAGES.map((l: { id: string }) => l.id).filter((id: string) => id !== "curl" && (!ONLY.length || ONLY.includes(id)));
// PRINT=1 prints the snippets without running them.
if (process.env.PRINT === "1") {
  for (const lang of langs) {
    for (const { c, model } of models) {
      const { code, note } = renderSnippet(lang, model, { fixedIv: false });
      console.log(`\n===== ${lang} · ${c.name}${note ? ` (note: ${note})` : ""}\n${code}`);
    }
  }
  process.exit(0);
}
const results: Record<string, Record<string, string>> = {};
let failed = 0;
for (const lang of langs) {
  results[lang] = {};
  const L = LANGS[lang]!;
  const dir = join(WORK, lang);
  if (!(await L.tool())) {
    for (const { c } of models) results[lang]![c.name] = "unavailable";
    console.log(`\n${lang}: toolchain unavailable`);
    continue;
  }
  mkdirSync(dir, { recursive: true });
  try {
    if (!existsSync(join(dir, ".ready"))) {
      await L.setup(dir);
      writeFileSync(join(dir, ".ready"), "");
    }
  } catch (err) {
    for (const { c } of models) results[lang]![c.name] = "setup failed";
    console.log(`\n${lang}: setup failed\n${(err as Error).message}`);
    failed++;
    continue;
  }
  console.log(`\n${lang}:`);
  for (const { c, model, expect: query, get } of models) {
    const expect = model.queryMethod && !QUERY_LANGUAGES.has(lang) ? get : query;
    const { code, note } = renderSnippet(lang, model, { fixedIv: true });
    if (!code) {
      results[lang]![c.name] = "note";
      console.log(`  ${c.name}: note only (${note})`);
      continue;
    }
    try {
      const got = await L.run(dir, code);
      const ok = got === expect;
      results[lang]![c.name] = ok ? "pass" : "FAIL";
      if (!ok) failed++;
      console.log(`  ${c.name}: ${ok ? "pass" : `FAIL\n    got    ${got}\n    expect ${expect}\n${code}`}`);
      if (VERBOSE) console.log(code);
    } catch (err) {
      results[lang]![c.name] = "error";
      failed++;
      console.log(`  ${c.name}: ERROR\n${(err as Error).message}\n--- snippet ---\n${code}`);
    }
  }
}

// The matrix: language × case.
const w = Math.max(...models.map(({ c }) => c.name.length));
console.log(`\n${"".padEnd(12)}${models.map(({ c }) => c.name.padEnd(w + 1)).join("")}`);
for (const lang of langs) console.log(`${lang.padEnd(12)}${models.map(({ c }) => (results[lang]![c.name] ?? "").padEnd(w + 1)).join("")}`);
if (process.env.XI_SNIPPETS_CLEAN === "1") rmSync(WORK, { recursive: true, force: true });
process.exit(failed ? 1 : 0);
