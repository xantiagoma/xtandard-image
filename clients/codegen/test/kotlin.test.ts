// Kotlin's leaf tokens and printer (WP-146).
//
//   bun test clients/codegen/test/kotlin.test.ts

import { describe, expect, test } from "bun:test";
import { camel, constant, kdocText, KotlinPrinter } from "../lang/kotlin.ts";
import { N } from "../model.ts";

const p = new KotlinPrinter();

describe("kotlin leaf tokens", () => {
  test("strings escape quotes, backslashes, control characters and $ templates", () => {
    expect(p.print(N.str('a"b\\c\nd'))).toBe('"a\\"b\\\\c\\nd"');
    expect(p.print(N.str("$x ${y}"))).toBe('"\\$x \\${y}"');
  });

  test("identifiers: camelCase, keywords get a suffix, leading digits a prefix", () => {
    expect(camel("min-width")).toBe("minWidth");
    expect(camel("R")).toBe("r");
    expect(camel("in")).toBe("inValue");
    expect(camel("object")).toBe("objectValue");
    expect(camel("3d")).toBe("v3d");
    expect(camel("")).toBe("value");
  });

  test("constants: UPPER_SNAKE, leading digits get a V", () => {
    expect(constant("fill-down")).toBe("FILL_DOWN");
    expect(constant("m3u8")).toBe("M3U8");
    expect(constant("3gp")).toBe("V3GP");
  });

  test("KDoc text cannot close the comment or link", () => {
    expect(kdocText("a */ b /* [x] `c`")).toBe("a *&#47; b &#47;* \\[x\\] 'c'");
  });

  test("numbers, booleans and null", () => {
    expect(p.print(N.call(N.id("listOf"), [N.int(16), N.num(1.5), N.bool(true), N.null()]))).toBe("listOf(16, 1.5, true, null)");
  });
});

describe("kotlin printer", () => {
  test("an expression-bodied method with a vararg and a spread", () => {
    const f = N.func(
      "crop",
      [N.param("width", N.tref("Number", [], { nullable: true })), N.param("rest", N.tref("Any"), { mods: ["variadic"] })],
      N.call(N.id("option"), [N.str("crop"), N.id("width"), N.spread(N.id("rest"))]),
      { returns: N.tref("S"), mods: ["public"], doc: N.comment("doc", ["Crops.", "", "@return this"]) },
    );
    expect(p.print(f)).toBe('/**\n * Crops.\n *\n * @return this\n */\npublic fun crop(width: Number?, vararg rest: Any): S = option("crop", width, *rest)');
  });

  test("a table constant starts on the line after =", () => {
    const c = N.const("NAMES", N.call(N.id("mapOf"), [N.binary("to", N.str("w"), N.str("width"))], { layout: "expanded" }), {
      type: N.tref("Map", [N.tref("String"), N.tref("String")]),
    });
    expect(p.print(c)).toBe('val NAMES: Map<String, String> =\n    mapOf(\n        "w" to "width",\n    )');
  });

  test("a trailing lambda", () => {
    const e = N.call(N.get(N.id("entries"), "firstOrNull"), [N.lambda([], N.binary("==", N.get(N.id("it"), "value"), N.id("value")))]);
    expect(p.print(e)).toBe("entries.firstOrNull { it.value == value }");
  });

  test("an enum class: entries separated by commas and closed by a semicolon", () => {
    const e = N.type("class", "Fmt", [N.member("A", N.str("a")), N.member("B", N.str("b")), N.blank(), N.func("toString", [], N.id("value"), { returns: N.tref("String"), mods: ["override"] })], {
      mods: ["public", "enum"],
      params: [N.param("value", N.tref("String"), { mods: ["public", "val"] })],
    });
    expect(p.print(e)).toBe('public enum class Fmt(\n    public val value: String,\n) {\n    A("a"),\n    B("b");\n\n    override fun toString(): String = value\n}');
  });

  test("a long argument list breaks at a narrow width", () => {
    const narrow = new (class extends KotlinPrinter {
      override width = 24;
    })();
    const call = N.call(N.id("listOf"), ["insecure", "unsafe", "_"].map(N.str));
    expect(p.print(call)).toBe('listOf("insecure", "unsafe", "_")');
    expect(narrow.print(call)).toBe('listOf(\n    "insecure",\n    "unsafe",\n    "_",\n)');
  });
});
