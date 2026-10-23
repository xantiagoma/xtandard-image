// Java's leaf tokens and printer (WP-146).
//
//   bun test clients/codegen/test/java.test.ts

import { describe, expect, test } from "bun:test";
import { camel, constant, J, JavaPrinter, jd } from "../lang/java.ts";
import { N } from "../model.ts";

const p = new JavaPrinter();

describe("java leaf tokens", () => {
  test("identifiers: camelCase, leading digits and keywords", () => {
    expect(camel("min-width")).toBe("minWidth");
    expect(camel("R")).toBe("r");
    expect(camel("3d")).toBe("v3d");
    expect(camel("class")).toBe("classValue");
    expect(camel("default")).toBe("defaultValue");
    expect(camel("--")).toBe("value");
  });

  test("constants: UPPER_SNAKE", () => {
    expect(constant("fill-down")).toBe("FILL_DOWN");
    expect(constant("m3u8")).toBe("M3U8");
    expect(constant("3d")).toBe("V3D");
  });

  test("strings escape quotes, backslashes and control characters", () => {
    expect(p.print(N.str('a"b\\c\nd'))).toBe('"a\\"b\\\\c\\nd"');
  });

  test("numbers print as JavaScript writes them", () => {
    expect(p.print(N.int(300))).toBe("300");
    expect(p.print(N.num(1.5))).toBe("1.5");
  });

  test("Javadoc text escapes HTML, tags, braces and terminators", () => {
    expect(jd("a<b> & c */ @x {y} \\")).toBe("a&lt;b&gt; &amp; c *&#47; &#64;x &#123;y&#125; &#92;");
  });
});

describe("java printer", () => {
  test("a one-line Javadoc is inline, a longer one a block", () => {
    expect(p.print(N.doc("One."))).toBe("/** One. */");
    expect(p.print(N.doc("One.\n\n@return x"))).toBe("/**\n * One.\n *\n * @return x\n */");
  });

  test("an expanded initializer continues four spaces deeper with the parenthesis on the last line", () => {
    const table = N.const(
      "NAMES",
      N.invoke(N.id("Map"), "ofEntries", [N.invoke(N.id("Map"), "entry", [N.str("a"), N.str("b")])], { layout: "expanded" }),
      { type: N.tref("Map", [N.tref("String"), N.tref("String")]), mods: ["static", "final"] },
    );
    expect(p.print(table)).toBe('static final Map<String, String> NAMES =\n    Map.ofEntries(\n        Map.entry("a", "b"));');
  });

  test("a record with nullable components and a constructor-free body", () => {
    const rec = N.type("record", "R", [], {
      mods: ["public"],
      params: [N.param("a", N.tref("long")), N.param("b", J.nullable(N.tref("String")))],
    });
    expect(p.print(rec)).toBe("public record R(\n    long a,\n    @Nullable String b) {}");
  });

  test("varargs, default methods and array creation", () => {
    const m = N.func(
      "crop",
      [N.param("w", N.tref("double")), N.param("rest", J.nullable(N.tref("Object")), { mods: ["variadic"] })],
      [N.ret(N.call(N.id("option"), [N.str("crop"), J.newArray(J.nullable(N.tref("Object")), [N.id("w")])]))],
      { returns: N.tref("S"), mods: ["default"] },
    );
    expect(p.print(m)).toBe('default S crop(double w, @Nullable Object... rest) {\n  return option("crop", new @Nullable Object[] {w});\n}');
  });

  test("a switch expression", () => {
    const s = N.ret(J.switchExpr(N.id("this"), [{ match: N.id("HLS"), value: N.str("m3u8") }]));
    expect(p.print(s)).toBe('return switch (this) {\n  case HLS -> "m3u8";\n};');
  });

  test("an argument list breaks past the width, continued four spaces deeper", () => {
    const narrow = new (class extends JavaPrinter {
      override width = 20;
    })();
    const call = N.invoke(N.id("List"), "of", ["alpha", "beta", "gamma", "delta"].map(N.str));
    expect(p.print(call)).toBe('List.of("alpha", "beta", "gamma", "delta")');
    expect(narrow.print(call)).toBe('List.of(\n    "alpha",\n    "beta",\n    "gamma",\n    "delta")');
  });
});
