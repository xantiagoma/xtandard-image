// Swift's leaf tokens and printer (WP-146).
//
//   bun test clients/codegen/test/swift.test.ts

import { describe, expect, test } from "bun:test";
import { any, array, camel, computed, dict, dot, ident, optional, property, SwiftPrinter } from "../lang/swift.ts";
import { N } from "../model.ts";

const p = new SwiftPrinter();

describe("swift leaf tokens", () => {
  test("strings escape backslashes (so \\( cannot interpolate), quotes and control characters", () => {
    expect(p.print(N.str("a\\(b)"))).toBe('"a\\\\(b)"');
    expect(p.print(N.str('q"\n\t\r'))).toBe('"q\\"\\n\\t\\r"');
    expect(p.print(N.str("\u0001\u007f ñ"))).toBe('"\\u{1}\\u{7f} ñ"');
  });

  test("identifiers: lowerCamelCase, leading digits, backticked keywords", () => {
    expect(camel("min-width")).toBe("minWidth");
    expect(camel("c_obj")).toBe("cObj");
    expect(camel("3d")).toBe("v3d");
    expect(ident("default")).toBe("`default`");
    expect(ident("extension")).toBe("`extension`");
    expect(property("request_id")).toBe("requestID");
  });

  test("numbers, nil and booleans", () => {
    expect(p.print(N.list([N.int(16), N.num(0.5), N.null(), N.bool(true)]))).toBe("[16, 0.5, nil, true]");
  });

  test("types", () => {
    expect(p.print(dict(N.tref("String"), array(N.tref("Int"))))).toBe("[String: [Int]]");
    expect(p.print(optional(any(N.tref("ArgConvertible"))))).toBe("(any ArgConvertible)?");
    expect(p.print(optional(N.tref("Int64")))).toBe("Int64?");
    expect(p.print(N.map([]))).toBe("[:]");
  });
});

describe("swift printer", () => {
  test("an enum with a computed property switching on self", () => {
    const e = N.enum("Kind", [N.member("a", N.str("a"), { doc: N.doc("A.") })], {
      mods: ["public"],
      supers: [N.tref("String")],
      body: [N.blank(), computed("status", N.tref("Int"), [N.switch(N.id("self"), [N.case([dot("a")], [N.stmt(N.int(1))])])], { mods: ["public"] })],
    });
    expect(p.print(e)).toBe(
      'public enum Kind: String {\n    /// A.\n    case a = "a"\n\n    public var status: Int {\n        switch self {\n        case .a: 1\n        }\n    }\n}',
    );
  });

  test("a long dictionary breaks at a narrow width", () => {
    const narrow = new (class extends SwiftPrinter {
      override width = 20;
    })();
    const m = N.map([N.entry(N.str("alpha"), N.str("a")), N.entry(N.str("beta"), N.str("b"))]);
    expect(p.print(m)).toBe('["alpha": "a", "beta": "b"]');
    expect(narrow.print(m)).toBe('[\n    "alpha": "a",\n    "beta": "b",\n]');
  });

  test("a protocol with no members and a variadic unlabeled parameter", () => {
    expect(p.print(N.type("protocol", "P", [], { mods: ["public"], supers: [N.tref("OptionChain")] }))).toBe("public protocol P: OptionChain {}");
    const f = N.func("q", [N.param("args", optional(any(N.tref("ArgConvertible"))), { mods: ["unlabeled", "variadic"] })], [N.stmt(N.call(N.id("option"), [N.str("q")]))], {
      mods: ["public"],
      returns: N.tref("Self"),
    });
    expect(p.print(f)).toBe('public func q(_ args: (any ArgConvertible)?...) -> Self {\n    option("q")\n}');
  });
});
