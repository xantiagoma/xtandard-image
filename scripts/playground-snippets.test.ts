// Unit tests of the playground's code snippets (internal/playground/assets/
// snippets.js and highlight.js, WP-145): every language renders every case
// of scripts/playground-snippets-cases.ts without a key in it, the
// highlighter keeps the text, and the TypeScript snippets, run with Bun
// against clients/typescript, print the reference URL. The other languages
// run in scripts/playground-snippets-check.ts (task playground:snippets).
//
//   bun test scripts/playground-snippets.test.ts
import { describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { CASES, CLIENTS, ENV, KEYS, reference } from "./playground-snippets-cases.ts";
// @ts-expect-error: plain ES module without types
import { tokenize } from "../internal/playground/assets/snippets/highlight.js";
// @ts-expect-error: plain ES module without types
import { LANGUAGES, renderSnippet, snippetModel, typedValue } from "../internal/playground/assets/snippets/index.js";

const models = CASES.map((c) => ({ c, model: c.model() }));
const langs: string[] = LANGUAGES.map((l: { id: string }) => l.id);
const secrets = [KEYS.signing.key, KEYS.signing.salt, KEYS.encryption.key, ENV.XI_PDF_PASSWORD];

describe("every language renders every case", () => {
  for (const lang of langs) {
    for (const { c, model } of models) {
      test(`${lang} · ${c.name}`, () => {
        const { code, note } = renderSnippet(lang, model, { fixedIv: false });
        expect(code || note).toBeTruthy();
        for (const s of secrets) expect(code).not.toContain(s);
        if (lang === "curl") {
          expect(code).toContain("curl ");
          return;
        }
        expect(code).not.toContain("XI_ENCRYPTION_IV"); // only with fixedIv
        if (model.signed) expect(code).toContain("XI_SIGNING_KEY");
        if (model.pdf) expect(code).toContain("XI_PDF_PASSWORD");
        if (model.kind !== "xisig") expect(code).toContain(model.source.slice(0, 20));
        const tokens = tokenize(code, lang);
        expect(tokens.map((t: { s: string }) => t.s).join("")).toBe(code);
        expect(tokens.some((t: { t: string }) => t.t === "s")).toBe(true);
        expect(tokens.some((t: { t: string }) => t.t === "k" || t.t === "f")).toBe(true);
      });
    }
  }
});

describe("the TypeScript snippets print the reference URL", () => {
  const dir = mkdtempSync(join(tmpdir(), "xi-snippets-ts-"));
  const pkg = join(dir, "node_modules/@xtandard/image");
  mkdirSync(pkg, { recursive: true });
  writeFileSync(join(pkg, "package.json"), JSON.stringify({ name: "@xtandard/image", type: "module", main: "index.ts" }));
  writeFileSync(join(pkg, "index.ts"), `export * from ${JSON.stringify(join(CLIENTS, "typescript/src/index.ts"))};\n`);
  for (const { c, model } of models) {
    test(c.name, async () => {
      const { code } = renderSnippet("typescript", model, { fixedIv: true });
      const file = join(dir, `${c.name}.ts`);
      writeFileSync(file, code);
      const p = Bun.spawn(["bun", file], { cwd: dir, env: { ...process.env, ...ENV }, stdout: "pipe", stderr: "pipe" });
      const out = (await new Response(p.stdout).text()).trim();
      expect(await p.exited).toBe(0);
      expect(out).toBe(await reference(model));
    });
  }
});

describe("model", () => {
  test("typedValue follows the catalogue type", () => {
    expect(typedValue("", "integer")).toEqual({ k: "null" });
    expect(typedValue("300", "integer")).toEqual({ k: "int", v: "300" });
    expect(typedValue("1.50", "number")).toMatchObject({ k: "num", v: "1.5" });
    expect(typedValue("2.0", "number")).toEqual({ k: "int", v: "2" });
    expect(typedValue("1", "boolean")).toEqual({ k: "bool", v: true });
    expect(typedValue("f", "boolean")).toEqual({ k: "bool", v: false });
    expect(typedValue("fill", "enum")).toMatchObject({ k: "str", v: "fill" });
    expect(typedValue("abc", "integer")).toMatchObject({ k: "str", v: "abc", t: "integer" });
  });

  test("a dialect without a client builder shows a note, and curl still works", () => {
    const m = { id: "imgix", grammar: "imgix", scheme: "imgix", prefix: "/imgix/", source: "web" };
    const state = { source: "https://e.com/a.jpg", pipelines: [[]], encoding: "plain", ext: "", raw: false, video: false, free: { path: "", query: "" } };
    const model = snippetModel({ m, state, index: new Map(), base: "http://h", signed: true, url: "http://h/imgix/a.jpg?w=1" });
    expect(model.kind).toBe("none");
    for (const lang of langs.filter((l) => l !== "curl")) {
      const { code, note } = renderSnippet(lang, model);
      expect(code).toBe("");
      expect(note).toContain("Official clients build imgproxy");
    }
    expect(renderSnippet("curl", model).code).toContain("http://h/imgix/a.jpg?w=1");
  });

  test("a repeated option in one pipeline is a note for the mapping forms", () => {
    const model = { kind: "image", url: "u", signed: false, base: "http://h", baseUrl: "http://h", source: "s", sourceEncoding: "plain", extension: "", pipelines: [[{ name: "blur", args: [{ k: "int", v: "1" }] }, { name: "blur", args: [{ k: "int", v: "2" }] }]], note: "" };
    expect(renderSnippet("python", model).code).toBe("");
    expect(renderSnippet("python", model).note).toContain("repeated");
    expect(renderSnippet("go", model).code).toContain("\t\tBlur(1).\n\t\tBlur(2).");
  });
});
