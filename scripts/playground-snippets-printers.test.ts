// Unit tests of the snippet generator's layers (WP-145): the Doc layout
// (internal/playground/assets/snippets/doc.js), the leaf tokens of every
// language's printer (string escaping against each language's interpolation,
// numbers, empty and nested collections), identifier casing, and line
// breaking at a narrow width. The generated programs themselves are run by
// scripts/playground-snippets-check.ts.
//
//   bun test scripts/playground-snippets-printers.test.ts
import { describe, expect, test } from "bun:test";
// @ts-expect-error: plain ES modules without types
import { concat, group, hardline, ifBreak, indent, join, line, print, softline } from "../internal/playground/assets/snippets/doc.js";
// @ts-expect-error: plain ES modules without types
import { camel, N, pascal, snake } from "../internal/playground/assets/snippets/model.js";
// @ts-expect-error: plain ES modules without types
import { goIdent } from "../internal/playground/assets/snippets/lang/go.js";

const langs = ["typescript", "python", "go", "php", "ruby", "rust", "java", "kotlin", "csharp", "elixir", "swift", "dart", "curl"];
const printers: Record<string, any> = {};
for (const id of langs) printers[id] = (await import(`../internal/playground/assets/snippets/lang/${id}.js`))[id].printer;

// expr prints one expression with a language's printer at a width.
function expr(lang: string, node: unknown, width = 80): string {
  const p = printers[lang];
  return print(p.expr(node), { width, unit: p.unit });
}

describe("doc layout", () => {
  const call = (args: string[]) => group(concat("f(", indent(concat(softline, join(concat(",", line), args))), ifBreak(","), softline, ")"));
  test("a group that fits stays on one line", () => {
    expect(print(call(["a", "b"]), { width: 20 })).toBe("f(a, b)");
  });
  test("a group that does not fit breaks every line, indented", () => {
    expect(print(call(["aaaa", "bbbb", "cccc"]), { width: 10 })).toBe("f(\n  aaaa,\n  bbbb,\n  cccc,\n)");
  });
  test("an outer group breaks while an inner one that fits stays flat", () => {
    const doc = group(concat("g(", indent(concat(softline, join(concat(",", line), [call(["x", "y"]), "zzzzzzzzzz"]))), softline, ")"));
    expect(print(doc, { width: 16 })).toBe("g(\n  f(x, y),\n  zzzzzzzzzz\n)");
  });
  test("a hard line breaks the groups around it", () => {
    expect(print(group(concat("a", line, "b", hardline, "c")), { width: 80 })).toBe("a\nb\nc");
  });
  test("shouldBreak breaks the group and its parents", () => {
    const inner = group(concat("[", indent(concat(softline, "x")), softline, "]"), { shouldBreak: true });
    expect(print(group(concat("(", indent(concat(softline, inner)), softline, ")")), { width: 80 })).toBe("(\n  [\n    x\n  ]\n)");
  });
  test("the rest of the line counts: a group is broken when what follows it would overflow", () => {
    expect(print(concat(call(["a", "b"]), "?;-----"), { width: 10 })).toBe("f(\n  a,\n  b,\n)?;-----");
  });
  test("tabs indent and count as tabWidth columns", () => {
    expect(print(call(["aaaa", "bbbb"]), { width: 8, unit: "\t" })).toBe("f(\n\taaaa,\n\tbbbb,\n)");
  });
});

describe("identifier casing", () => {
  test("Go initialisms follow the Go emitter", () => {
    expect(goIdent("watermark_url")).toBe("WatermarkURL");
    expect(goIdent("avif_options")).toBe("AVIFOptions");
    expect(goIdent("min-width")).toBe("MinWidth");
    expect(goIdent("pdf_password")).toBe("PDFPassword");
  });
  test("camel, Pascal and snake case", () => {
    expect(camel("format_quality")).toBe("formatQuality");
    expect(camel("min-width")).toBe("minWidth");
    expect(pascal("min-width")).toBe("MinWidth");
    expect(snake("infoUrl")).toBe("info_url");
  });
});

// A string with every character some language interpolates or escapes.
const tricky = "a\"b'c\\d $x #{y} \\(z) é\n\u0001";

describe("string escaping per language", () => {
  const want: Record<string, string> = {
    typescript: '"a\\"b\'c\\\\d $x #{y} \\\\(z) é\\n\\u0001"',
    python: '"a\\"b\'c\\\\d $x #{y} \\\\(z) é\\n\\u0001"',
    go: '"a\\"b\'c\\\\d $x #{y} \\\\(z) é\\n\\u0001"',
    php: "'a\"b\\'c\\\\d $x #{y} \\\\(z) é\n\u0001'",
    ruby: '"a\\"b\'c\\\\d $x \\#{y} \\\\(z) é\\n\\u0001"',
    rust: '"a\\"b\'c\\\\d $x #{y} \\\\(z) é\\n\\u{0001}"',
    java: '"a\\"b\'c\\\\d $x #{y} \\\\(z) é\\n\\u0001"',
    kotlin: '"a\\"b\'c\\\\d \\$x #{y} \\\\(z) é\\n\\u0001"',
    csharp: '"a\\"b\'c\\\\d $x #{y} \\\\(z) é\\n\\u0001"',
    elixir: '"a\\"b\'c\\\\d $x \\#{y} \\\\(z) é\\n\\u0001"',
    swift: '"a\\"b\'c\\\\d $x #{y} \\\\(z) é\\n\\u{0001}"',
    dart: "'a\"b\\'c\\\\d \\$x #{y} \\\\(z) é\\n\\u{1}'",
    curl: "'a\"b'\\''c\\d $x #{y} \\(z) é\n\u0001'",
  };
  for (const lang of langs) {
    test(lang, () => expect(expr(lang, N.str(tricky))).toBe(want[lang]!));
  }
  test("a backslash before u is not taken for an escape (\\u{…} languages)", () => {
    expect(expr("rust", N.str("\\u0041"))).toBe('"\\\\u0041"');
    expect(expr("swift", N.str("\\u0041"))).toBe('"\\\\u0041"');
  });
});

describe("literals per language", () => {
  const lit = (k: string, v?: unknown) => N.lit({ k, v });
  test("null and booleans", () => {
    const got = Object.fromEntries(langs.filter((l) => l !== "curl").map((l) => [l, [expr(l, lit("null")), expr(l, lit("bool", true))].join(" ")]));
    expect(got).toEqual({
      typescript: "null true", python: "None True", go: "nil true", php: "null true", ruby: "nil true", rust: "null true",
      java: "null true", kotlin: "null true", csharp: "null true", elixir: "nil true", swift: "nil true", dart: "null true",
    });
  });
  test("numbers are written as the spec's JavaScript form; Java and C# mark longs", () => {
    for (const l of langs) expect(expr(l, lit("num", "0.5"))).toBe("0.5");
    expect(expr("java", lit("int", "3000000000"))).toBe("3000000000L");
    expect(expr("csharp", lit("int", "3000000000"))).toBe("3000000000L");
    expect(expr("python", lit("int", "3000000000"))).toBe("3000000000");
  });
});

describe("collections per language", () => {
  const entries = (n: number) => Array.from({ length: n }, (_, i) => N.entry(N.str(i ? `key_${i}` : "min-width"), N.int(i)));
  test("empty", () => {
    expect(expr("swift", N.map([]))).toBe("[:]");
    expect(expr("python", N.map([]))).toBe("{}");
    expect(expr("php", N.map([]))).toBe("[]");
    expect(expr("elixir", N.map([], "keyword"))).toBe("[]");
  });
  test("keys that are not identifiers are quoted", () => {
    expect(expr("typescript", N.map(entries(2)))).toBe('{ "min-width": 0, key_1: 1 }');
    expect(expr("ruby", N.map(entries(2)))).toBe('{ "min-width": 0, key_1: 1 }');
    expect(expr("elixir", N.map(entries(2), "keyword"))).toBe('["min-width": 0, key_1: 1]');
    expect(expr("php", N.map(entries(2)))).toBe("['min-width' => 0, 'key_1' => 1]");
  });
  test("nested collections break from the outside in at a narrow width", () => {
    const nested = N.list([N.map(entries(3)), N.map(entries(1))]);
    expect(expr("python", nested, 50)).toBe('[\n    {"min-width": 0, "key_1": 1, "key_2": 2},\n    {"min-width": 0},\n]');
    expect(expr("dart", nested, 30)).toBe("[\n  {\n    'min-width': 0,\n    'key_1': 1,\n    'key_2': 2,\n  },\n  {'min-width': 0},\n]");
  });
  test("tuples: Python, Rust and Elixir", () => {
    const t = N.list([N.str("fill"), N.int(300)], "tuple");
    expect(expr("python", t)).toBe('("fill", 300)');
    expect(expr("rust", t)).toBe('("fill", 300)');
    expect(expr("elixir", t)).toBe('{"fill", 300}');
  });
});

describe("calls break at a narrow width", () => {
  const call = N.call(N.member(N.id("img"), "url"), [N.str("s3://bucket/a-long-source-name.jpg"), N.named("extension", N.str("webp"))]);
  test("trailing commas where the language allows them", () => {
    expect(expr("python", call, 40)).toBe('img.url(\n    "s3://bucket/a-long-source-name.jpg",\n    extension="webp",\n)');
    expect(expr("csharp", call, 40)).toBe('img.url(\n    "s3://bucket/a-long-source-name.jpg",\n    extension: "webp"\n)');
    expect(expr("kotlin", call, 40)).toBe('img.url(\n    "s3://bucket/a-long-source-name.jpg",\n    extension = "webp",\n)');
  });
  test("a chain of calls breaks one call per line (Go keeps the dot at the end)", () => {
    const chain = ["Resize", "Gravity", "Quality"].reduce((o: unknown, m) => N.call(N.member(o, m), [N.int(1)]), N.call(N.member(N.id("img"), "Image"), [N.str("a.jpg")]));
    expect(expr("go", chain)).toBe('img.Image("a.jpg").\n\tResize(1).\n\tGravity(1).\n\tQuality(1)');
    expect(expr("java", chain)).toBe('img.Image("a.jpg")\n    .Resize(1)\n    .Gravity(1)\n    .Quality(1)');
  });
  test("short chains stay on one line", () => {
    const chain = N.call(N.member(N.call(N.member(N.type("Options"), "new", { static: true })), "set"), [N.str("w"), N.int(300)]);
    expect(expr("rust", chain)).toBe('Options::new().set("w", 300)');
  });
});
