// Dart's leaf tokens and printer (WP-146).
//
//   bun test clients/codegen/test/dart.test.ts

import { describe, expect, test } from "bun:test";
import { camel, DartPrinter, ident, md, quote } from "../lang/dart.ts";
import { N } from "../model.ts";

const p = new DartPrinter();

describe("dart leaf tokens", () => {
  test("single-quoted strings escape quotes, backslashes, $ and newlines", () => {
    expect(quote("it's")).toBe("'it\\'s'");
    expect(quote("a\\b")).toBe("'a\\\\b'");
    expect(quote("$x ${y}")).toBe("'\\$x \\${y}'");
    expect(quote("a\nb")).toBe("'a\\nb'");
    expect(p.print(N.str('"ñ"'))).toBe("'\"ñ\"'");
  });

  test("identifiers: lowerCamelCase, reserved words get an underscore", () => {
    expect(camel("min-width")).toBe("minWidth");
    expect(camel("c_obj")).toBe("cObj");
    expect(camel("R")).toBe("r");
    expect(ident("if")).toBe("if_");
    expect(ident("default")).toBe("default_");
    expect(ident("class")).toBe("class_");
  });

  test("numbers, booleans and null", () => {
    expect(p.print(N.list([N.int(300), N.num(0.5), N.num(1e21), N.bool(true), N.null()]))).toBe("[300, 0.5, 1e+21, true, null]");
  });

  test("dartdoc Markdown is escaped outside code spans", () => {
    expect(md("a [b] <c> `[d] <e>`")).toBe("a \\[b\\] &lt;c&gt; `[d] <e>`");
  });
});

describe("dart printer", () => {
  test("a constructor with optional parameters and an initializer list", () => {
    const f = N.func(
      "Opt.resize",
      [N.param("mode", N.tref("String", [], { nullable: true })), N.param("width", N.tref("int", [], { nullable: true }), { mods: ["optional"] })],
      N.ext("dart.initializers", { items: [N.call(N.get(N.id("this"), "_"), [N.str("resize"), N.list([N.id("mode"), N.id("width")])])] }),
    );
    expect(p.print(f)).toBe("Opt.resize(String? mode, [int? width]) : this._('resize', [mode, width]);");
  });

  test("an enum with members, a constructor and a lookup", () => {
    const e = N.enum("Kind", [N.member("a", undefined, { args: [N.str("a")] }), N.member("b", undefined, { args: [N.str("b")] })], {
      body: [
        N.blank(),
        N.func("Kind", [N.param("value", undefined, { mods: ["this"] })], undefined, { mods: ["const"] }),
        N.blank(),
        N.func("fromValue", [N.param("value", N.tref("String"))], [N.forEach("k", N.id("values"), [N.if(N.binary("==", N.get(N.id("k"), "value"), N.id("value")), [N.ret(N.id("k"))])]), N.ret(N.null())], {
          returns: N.tref("Kind", [], { nullable: true }),
          mods: ["static"],
        }),
      ],
    });
    expect(p.print(e)).toBe(
      "enum Kind {\n  a('a'),\n  b('b');\n\n  const Kind(this.value);\n\n  static Kind? fromValue(String value) {\n    for (final k in values) {\n      if (k.value == value) return k;\n    }\n    return null;\n  }\n}",
    );
  });

  test("a const list breaks one item per line at a narrow width", () => {
    const narrow = new (class extends DartPrinter {
      override width = 30;
    })();
    const c = N.const("formats", N.list(["jpeg", "png", "webp", "avif"].map(N.str)), { type: N.tref("List", [N.tref("String")]), mods: ["const"] });
    expect(p.print(c)).toBe("const List<String> formats = ['jpeg', 'png', 'webp', 'avif'];");
    expect(narrow.print(c)).toBe("const List<String> formats = [\n  'jpeg',\n  'png',\n  'webp',\n  'avif',\n];");
  });

  test("records, ternaries and collection-for", () => {
    expect(p.print(N.ext("dart.record", { fields: [{ name: "x", type: N.tref("num") }, { name: "y", type: N.tref("num") }] }))).toBe("({num x, num y})");
    expect(p.print(N.ext("dart.ternary", { cond: N.id("c"), then: N.null(), else: N.id("v") }))).toBe("c ? null : v");
    expect(p.print(N.list([N.ext("dart.for-element", { name: "g", iterable: N.id("gs"), body: N.spread(N.list([N.get(N.id("g"), "x")])) })]))).toBe("[for (final g in gs) ...[g.x]]");
  });
});
