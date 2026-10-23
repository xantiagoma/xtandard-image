// TypeScript's leaf tokens and printer (WP-146), used by
// clients/typescript/scripts/generate.ts for src/generated/options.ts.
//
//   bun test clients/codegen/test/typescript.test.ts

import { describe, expect, test } from "bun:test";
import { jsdocText, label, TS, TypeScriptPrinter } from "../lang/typescript.ts";
import { N } from "../model.ts";

const p = new TypeScriptPrinter();

describe("typescript leaf tokens", () => {
  test("tuple labels: invalid characters, leading digits and reserved words", () => {
    expect(label("min-width")).toBe("min_width");
    expect(label("3d")).toBe("3d_");
    expect(label("default")).toBe("default_");
    expect(label("$x")).toBe("$x");
  });

  test("strings escape quotes, backslashes and control characters; template markers stay plain", () => {
    expect(p.print(N.str('a"b\\c\nd'))).toBe('"a\\"b\\\\c\\nd"');
    expect(p.print(N.str("${x} `y`"))).toBe('"${x} `y`"');
  });

  test("numbers print as JavaScript writes them", () => {
    expect(p.print(N.num(0.5))).toBe("0.5");
    expect(p.print(N.int(300))).toBe("300");
    expect(p.print(N.tlit(N.scalar(1e21)))).toBe("1e+21");
  });

  test("JSDoc text cannot close its comment", () => {
    expect(jsdocText("a */ b")).toBe("a *\\/ b");
    expect(p.print(N.doc("one */"))).toBe("/** one *\\/ */");
    expect(p.print(N.comment("doc", ["one", "", "two"]))).toBe("/**\n * one\n *\n * two\n */");
  });
});

describe("typescript printer", () => {
  test("labelled, optional and rest tuple elements", () => {
    const t = N.ttuple([
      TS.element("mode", N.tunion([N.tlit(N.str("fill")), N.tref("null")])),
      TS.element("width", N.tref("number"), { optional: true }),
      TS.element("rest", N.tref("Array", [N.tref("number")]), { rest: true }),
    ]);
    expect(p.print(t)).toBe('[mode: "fill" | null, width?: number, ...rest: Array<number>]');
  });

  test("an interface property whose union is one member per line", () => {
    const i = N.type("interface", "Args", [N.field("q", TS.linesUnion([N.ttuple([TS.element("q", N.tref("number"))]), N.ttuple([])]), { mods: ["quoted"] })], {
      mods: ["export"],
    });
    expect(p.print(i)).toBe('export interface Args {\n  "q":\n    | [q: number]\n    | [];\n}');
  });

  test("an as-const table", () => {
    const c = N.const("names", TS.asConst(N.list([N.str("a")], { layout: "expanded" })), { mods: ["export"], doc: N.doc("Names.") });
    expect(p.print(c)).toBe('/** Names. */\nexport const names = [\n  "a",\n] as const;');
  });

  test("a long tuple breaks at a narrow width", () => {
    const narrow = new (class extends TypeScriptPrinter {
      override width = 24;
    })();
    const t = N.ttuple(["alpha", "beta", "gamma"].map((n) => TS.element(n, N.tref("number"))));
    expect(p.print(t)).toBe("[alpha: number, beta: number, gamma: number]");
    expect(narrow.print(t)).toBe("[\n  alpha: number,\n  beta: number,\n  gamma: number\n]");
  });
});
