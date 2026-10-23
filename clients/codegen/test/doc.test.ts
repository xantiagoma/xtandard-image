// The Doc algebra is one module (WP-146): the generator's doc.ts re-exports
// the playground's doc.js, and the playground's snippet printers import that
// same file. Plus the layout helpers doc.ts adds.
//
//   bun test clients/codegen/test/doc.test.ts

import { describe, expect, test } from "bun:test";
import { Printer as SnippetPrinter } from "../../../internal/playground/assets/snippets/printer.js";
import * as playground from "../../../internal/playground/assets/snippets/doc.js";
import * as doc from "../doc.ts";

const { block, bracket, concat, group, hardline, indent, line, lines, paragraphs, print, softline } = doc;

describe("one Doc module", () => {
  test("doc.ts re-exports the playground's doc.js", () => {
    for (const name of ["concat", "group", "indent", "ifBreak", "join", "line", "softline", "hardline", "print"] as const) {
      expect(doc[name] as unknown).toBe(playground[name] as unknown);
    }
  });

  test("the playground's printers lay out with the same module", async () => {
    const src = await Bun.file(new URL("../../../internal/playground/assets/snippets/printer.js", import.meta.url)).text();
    expect(src).toContain("from './doc.js'");
    // Printing through the snippet printer and through doc.ts gives the same text.
    const p = new SnippetPrinter();
    expect(p.print({ k: "program", imports: [], header: [], body: [{ k: "stmt", expr: { k: "id", name: "x" } }] })).toBe(print(concat("x", hardline)));
  });
});

describe("layout helpers", () => {
  test("lines and paragraphs", () => {
    expect(print(lines(["a", "b"]))).toBe("a\nb");
    expect(print(paragraphs(["a", "b"]))).toBe("a\n\nb");
  });

  test("block indents its body one level", () => {
    expect(print(block("f {", ["a", "b"], "}"), { unit: "    " })).toBe("f {\n    a\n    b\n}");
    expect(print(block("f {", [], "}"))).toBe("f {\n}");
  });

  test("bracket stays on one line when it fits and breaks one item per line when not", () => {
    const items = ["alpha", "beta", "gamma"];
    expect(print(bracket("[", items, "]"), { width: 80 })).toBe("[alpha, beta, gamma]");
    expect(print(bracket("[", items, "]"), { width: 10 })).toBe("[\n  alpha,\n  beta,\n  gamma,\n]");
    expect(print(bracket("[", items, "]", { trailing: false }), { width: 10 })).toBe("[\n  alpha,\n  beta,\n  gamma\n]");
    expect(print(bracket("{", items, "}", { pad: true }), { width: 80 })).toBe("{ alpha, beta, gamma }");
    expect(print(bracket("[", [], "]"))).toBe("[]");
  });

  test("force breaks a bracket that would fit", () => {
    expect(print(bracket("(", ["a"], ")", { force: true }))).toBe("(\n  a,\n)");
  });

  test("an unbounded width never breaks a group; a hard line breaks the groups around it", () => {
    const long = group(concat("f(", indent(concat(softline, "x".repeat(500), ",", line, "y")), softline, ")"));
    expect(print(long, { width: Number.POSITIVE_INFINITY })).toBe(`f(${"x".repeat(500)}, y)`);
    expect(print(group(concat("a", line, "b", hardline, "c")), { width: 80 })).toBe("a\nb\nc");
  });

  test("empty lines carry no indentation", () => {
    expect(print(indent(concat("a", hardline, hardline, "b")), { unit: "\t" })).toBe("a\n\n\tb");
  });
});
