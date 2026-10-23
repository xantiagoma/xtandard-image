// Go's leaf tokens and printer (WP-146).
//
//   bun test clients/codegen/test/go.test.ts

import { describe, expect, test } from "bun:test";
import { G, GoPrinter, ident, structTag } from "../lang/go.ts";
import { N } from "../model.ts";

const p = new GoPrinter();

describe("go leaf tokens", () => {
  test("identifiers: initialisms, fixed casings, dashes and leading digits", () => {
    expect(ident("pdf_password")).toBe("PDFPassword");
    expect(ident("min-width")).toBe("MinWidth");
    expect(ident("webp_options")).toBe("WebPOptions");
    expect(ident("class_id")).toBe("ClassID");
    expect(ident("xi_sig")).toBe("XiSig");
    expect(ident("3d")).toBe("V3d");
    expect(() => ident("--")).toThrow("no identifier");
  });

  test("strings escape quotes, backslashes and control characters", () => {
    expect(p.print(N.str('a"b\\c\nd\t'))).toBe('"a\\"b\\\\c\\nd\\t"');
    expect(p.print(N.str("ñ ∞"))).toBe('"ñ ∞"');
  });

  test("numbers, nil and booleans", () => {
    expect(p.print(N.int(1073741824))).toBe("1073741824");
    expect(p.print(N.num(0.5))).toBe("0.5");
    expect(p.print(N.null())).toBe("nil");
    expect(p.print(N.bool(true))).toBe("true");
  });

  test("struct tags are raw strings", () => {
    expect(structTag("json", "class_id,omitempty")).toBe('`json:"class_id,omitempty"`');
    expect(() => structTag("json", 'a"b')).toThrow("escaping");
  });
});

describe("go printer", () => {
  test("types: pointers, slices, maps, results", () => {
    expect(p.print(N.tref("int", [], { nullable: true }))).toBe("*int");
    expect(p.print(G.slice(G.slice(N.tref("float64"))))).toBe("[][]float64");
    expect(p.print(G.map(N.tref("string"), N.tref("any")))).toBe("map[string]any");
    expect(p.print(N.ttuple([N.tref("string"), N.tref("bool")]))).toBe("(string, bool)");
  });

  test("a method with a receiver, a switch and composite literals", () => {
    const f = N.func(
      "Protocols",
      [],
      [N.switch(N.id("f"), [N.case([N.str("ts")], [N.ret(N.list([N.str("hls")], { type: G.slice(N.tref("StreamProtocol")) }))])]), N.ret(N.null())],
      { receiver: N.param("f", N.tref("SegmentFormat")), returns: G.slice(N.tref("StreamProtocol")), doc: N.doc("Protocols returns them.") },
    );
    expect(p.print(f)).toBe(
      '// Protocols returns them.\nfunc (f SegmentFormat) Protocols() []StreamProtocol {\n\tswitch f {\n\t\tcase "ts":\n\t\t\treturn []StreamProtocol{"hls"}\n\t}\n\treturn nil\n}',
    );
  });

  test("const groups, inline functions and trailing comments", () => {
    const g = G.group("const", [N.const("A", N.str("a"), { type: N.tref("K"), doc: N.doc("A is a.") })], N.doc("Values of K."));
    expect(p.print(g)).toBe('// Values of K.\nconst (\n\t// A is a.\n\tA K = "a"\n)');
    expect(p.print(N.const("X", N.int(1)))).toBe("const X = 1");
    const inline = N.func("Error", [], [N.ret(N.call(N.id("string"), [N.id("k")]))], { receiver: N.param("k", N.tref("K")), returns: N.tref("string"), mods: ["inline"] });
    expect(p.print(inline)).toBe("func (k K) Error() string { return string(k) }");
    expect(p.print(G.trailing(N.typeAlias("K", N.tref("string")), "nolint:errname"))).toBe("type K string //nolint:errname");
  });

  test("a long composite literal breaks one element per line at a narrow width", () => {
    const narrow = new (class extends GoPrinter {
      override width = 30;
    })();
    const lit = N.new(N.tref("xtandard.Option"), [N.named("Name", N.str("resize")), N.named("Args", N.id("args"))]);
    expect(p.print(lit)).toBe('xtandard.Option{Name: "resize", Args: args}');
    expect(narrow.print(lit)).toBe('xtandard.Option{\n\tName: "resize",\n\tArgs: args,\n}');
  });

  test("variadic parameters and spread arguments", () => {
    const f = N.func("Q", [N.param("args", N.tref("xtandard.Arg"), { mods: ["variadic"] })], [N.ret(N.invoke(N.id("c"), "Option", [N.str("q"), N.spread(N.id("args"))]))], {
      receiver: N.param("c", N.tref("C", [], { nullable: true })),
      returns: N.tref("C", [], { nullable: true }),
    });
    expect(p.print(f)).toBe('func (c *C) Q(args ...xtandard.Arg) *C {\n\treturn c.Option("q", args...)\n}');
  });
});
