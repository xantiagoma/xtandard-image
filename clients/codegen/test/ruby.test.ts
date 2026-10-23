// Ruby's and RBS's leaf tokens and printers (WP-146).
//
//   bun test clients/codegen/test/ruby.test.ts

import { describe, expect, test } from "bun:test";
import { ident, R, RbsPrinter, rbSymbol, recordKey, regexp, RubyPrinter, typeName } from "../lang/ruby.ts";
import { N } from "../model.ts";

const rb = new RubyPrinter();
const rbs = new RbsPrinter();

describe("ruby leaf tokens", () => {
  test("double-quoted strings escape quotes, backslashes, control characters and #{", () => {
    expect(rb.print(N.str('a"b\\c\nd'))).toBe('"a\\"b\\\\c\\nd"');
    expect(rb.print(N.str("#{x} and #"))).toBe('"\\#{x} and \\#"');
  });

  test("symbols are bare when they are plain names", () => {
    expect(rbSymbol("fill")).toBe(":fill");
    expect(rbSymbol("fill-down")).toBe(':"fill-down"');
    expect(rbSymbol("a#b")).toBe(':"a\\#b"');
  });

  test("identifiers", () => {
    expect(ident("min-width")).toBe("min_width");
    expect(ident("3d")).toBe("_3d");
    expect(typeName("InfoRGB")).toBe("info_rgb");
    expect(typeName("ErrorResponse")).toBe("error_response");
  });

  test("regexp literals anchor on the whole string and escape slashes", () => {
    expect(regexp("^[a-z]+/x$")).toBe("/\\A[a-z]+\\/x\\z/");
    expect(regexp("a\\$")).toBe("/a\\$/");
    expect(rb.print(R.regexp("^a$"))).toBe("/\\Aa\\z/");
  });

  test("literals, frozen collections and splats", () => {
    expect(rb.print(N.list([N.null(), N.bool(true), N.int(3)]))).toBe("[nil, true, 3]");
    expect(rb.print(N.invoke(N.map([N.entry(N.str("a"), N.int(1))], { layout: "expanded" }), "freeze"))).toBe('{\n  "a" => 1,\n}.freeze');
    expect(rb.print(N.call(N.id("option"), [N.str("q"), N.spread(N.id("args"))]))).toBe('option("q", *args)');
  });
});

describe("rbs leaf tokens", () => {
  test("string and symbol literal types are always quoted", () => {
    expect(rbs.print(N.tunion([N.tlit(N.str("a#b")), N.tlit(N.sym("fill-down"))]))).toBe('"a#b" | :"fill-down"');
  });

  test("record keys", () => {
    expect(recordKey("width")).toBe("width:");
    expect(recordKey("min-width")).toBe(':"min-width" =>');
  });

  test("optional types are parenthesized when a union is inside", () => {
    expect(rbs.print(R.optional(N.tref("String")))).toBe("String?");
    expect(rbs.print(R.optional(N.tunion([N.tref("Integer"), N.tref("Float")])))).toBe("(Integer | Float)?");
    expect(rbs.print(R.optional(N.tref("Array", [N.tunion([N.tref("A"), N.tref("B")])])))).toBe("(Array[A | B])?");
  });
});

describe("rbs printer", () => {
  test("overloads line up under the first", () => {
    const self = N.tref("self");
    const m = R.method("q", [
      R.overload([N.param("", N.tref("Integer"))], self),
      R.overload([N.param("", N.tunion([N.tref("Integer"), N.tref("Float")]), { mods: ["variadic"] })], self),
    ]);
    expect(rbs.print(N.namespace("M", [m]))).toBe("module M\n  def q: (Integer) -> self\n        | (*(Integer | Float)) -> self\nend");
  });

  test("records put one field per line without a trailing comma", () => {
    const rec = R.record([
      { name: "w", key: "symbol", optional: true, type: N.tref("Integer") },
      { name: "kind", key: "string", optional: false, type: N.tref("String") },
    ]);
    expect(rbs.print(N.typeAlias("r", rec))).toBe('type r = {\n  ?w: Integer,\n  "kind" => String\n}');
  });

  test("a frozen array breaks at a narrow width", () => {
    const narrow = new (class extends RubyPrinter {
      override width = 20;
    })();
    const list = N.invoke(N.list(["jpeg", "png", "webp"].map(N.str)), "freeze");
    expect(rb.print(list)).toBe('["jpeg", "png", "webp"].freeze');
    expect(narrow.print(list)).toBe('[\n  "jpeg",\n  "png",\n  "webp",\n].freeze');
  });
});
