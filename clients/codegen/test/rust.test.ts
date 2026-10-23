// Rust's leaf tokens and printer (WP-146).
//
//   bun test clients/codegen/test/rust.test.ts

import { describe, expect, test } from "bun:test";
import { code, field, ident, md, R, RustPrinter, rustChar, rustString, variant } from "../lang/rust.ts";
import { N } from "../model.ts";

const p = new RustPrinter();

describe("rust leaf tokens", () => {
  test("method identifiers: dashes, case, leading digits and keywords", () => {
    expect(ident("min-width")).toBe("min_width");
    expect(ident("crop_AR")).toBe("crop_ar");
    expect(ident("3d")).toBe("_3d");
    expect(ident("type")).toBe("type_");
  });

  test("fields are raw identifiers when they are keywords", () => {
    expect(field("type")).toBe("r#type");
    expect(field("Width")).toBe("width");
  });

  test("variants are PascalCase and never start with a digit", () => {
    expect(variant("fill-down")).toBe("FillDown");
    expect(variant("h265")).toBe("H265");
    expect(variant("3d")).toBe("V3d");
    expect(() => variant("--")).toThrow();
  });

  test("strings and chars escape quotes, backslashes and control characters", () => {
    expect(rustString('a"b\\c\nd\t')).toBe('"a\\"b\\\\c\\nd\\t"');
    expect(rustString("\u0001 ñ {x}")).toBe('"\\u{1} ñ {x}"');
    expect(rustChar("'")).toBe("'\\''");
    expect(rustChar("\\")).toBe("'\\\\'");
    expect(rustChar(":")).toBe("':'");
    expect(() => rustChar("ab")).toThrow();
  });

  test("Markdown escaping leaves code spans alone", () => {
    expect(md("a [b] <c> `[d]`")).toBe("a \\[b\\] \\<c\\> `[d]`");
    expect(code("x:y")).toBe("`x:y`");
    expect(code("a `b`")).toBe("a `b`");
  });

  test("numbers print as JavaScript writes them", () => {
    expect(p.print(N.list([N.int(16), N.num(0.5)]))).toBe("[16, 0.5]");
  });
});

describe("rust printer", () => {
  test("constants with reference and slice types", () => {
    const pair = R.ref(R.slice(N.ttuple([R.ref(N.tref("str")), R.ref(N.tref("str"))])));
    const c = N.const("NAMES", N.unary("&", N.list([N.tuple([N.str("a"), N.str("b")])], { layout: "expanded" })), {
      type: pair,
      mods: ["pub"],
      doc: N.doc("The names."),
    });
    expect(p.print(c)).toBe('/// The names.\npub const NAMES: &[(&str, &str)] = &[\n    ("a", "b"),\n];');
  });

  test("a match function and an expression-bodied one", () => {
    const f = N.func("status", [N.param("self")], [N.switch(N.id("self"), [N.case([N.get(N.id("Self"), "A", { mods: ["static"] })], [N.stmt(N.int(404))])])], {
      mods: ["pub", "const"],
      returns: N.tref("u16"),
    });
    expect(p.print(f)).toBe("pub const fn status(self) -> u16 {\n    match self {\n        Self::A => 404,\n    }\n}");
    const g = N.func("m", [N.param("self"), N.param("args", R.impl(N.tref("$crate::IntoArgs")))], N.invoke(N.id("self"), "option", [N.str("q"), N.id("args")]), {
      mods: ["pub"],
      returns: N.tref("Self"),
    });
    expect(p.print(g)).toBe('pub fn m(self, args: impl $crate::IntoArgs) -> Self {\n    self.option("q", args)\n}');
  });

  test("str_enum! and attributes", () => {
    const e = R.strEnum(N.enum("Fmt", [N.member("Fmp4", R.alternatives([N.str("fmp4"), N.str("cmaf")]))], { mods: ["pub"] }), "InvalidRequest");
    expect(p.print(e)).toBe('crate::str_enum! {\n    pub enum Fmt (InvalidRequest) {\n        Fmp4 = "fmp4" | "cmaf",\n    }\n}');
    expect(p.print(N.annotation("serde", [N.binary("=", N.id("rename"), N.str("X")), N.id("default")]))).toBe('#[serde(rename = "X", default)]');
  });

  test("a struct literal and a slice break at a narrow width", () => {
    const narrow = new (class extends RustPrinter {
      override width = 30;
    })();
    const lit = N.new(N.tref("Catalogue"), [N.named("kind", N.str("imgproxy")), N.named("names", N.id("NAMES"))]);
    expect(p.print(lit)).toBe('Catalogue { kind: "imgproxy", names: NAMES }');
    expect(narrow.print(lit)).toBe('Catalogue {\n    kind: "imgproxy",\n    names: NAMES,\n}');
    expect(narrow.print(N.unary("&", N.list(["jpeg", "png", "webp", "avif"].map(N.str))))).toBe('&[\n    "jpeg",\n    "png",\n    "webp",\n    "avif",\n]');
  });
});
