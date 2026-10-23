// Python's leaf tokens and printer (WP-146).
//
//   bun test clients/codegen/test/python.test.ts

import { describe, expect, test } from "bun:test";
import { ident, PythonPrinter } from "../lang/python.ts";
import { N } from "../model.ts";

const p = new PythonPrinter();

describe("python leaf tokens", () => {
  test("identifiers: dashes, leading digits and keywords", () => {
    expect(ident("min-width")).toBe("min_width");
    expect(ident("3d")).toBe("_3d");
    expect(ident("if")).toBe("if_");
    expect(ident("None")).toBe("None_");
  });

  test("strings escape quotes, backslashes and control characters", () => {
    expect(p.print(N.str('a"b\\c\nd\t'))).toBe('"a\\"b\\\\c\\nd\\t"');
    expect(p.print(N.str("{x} ñ"))).toBe('"{x} ñ"');
  });

  test("literals", () => {
    expect(p.print(N.tuple([N.null(), N.bool(true), N.bool(false)]))).toBe("(None, True, False)");
    expect(p.print(N.tuple([N.int(1)]))).toBe("(1,)");
    expect(p.print(N.tuple([]))).toBe("()");
  });

  test("docstrings escape triple quotes and backslashes", () => {
    expect(p.print(N.doc('say """hi""" \\ there'))).toBe('"""say \\"\\"\\"hi\\"\\"\\" \\\\ there"""');
    expect(p.print(N.doc("one\ntwo"))).toBe('"""one\ntwo\n"""');
  });
});

describe("python printer", () => {
  test("a class with a docstring, a method and a keyword in its bases", () => {
    const cls = N.type(
      "class",
      "Box",
      [N.blank(), N.func("f", [N.param("self"), N.param("args", N.tref("int"), { mods: ["variadic"] })], [N.ret(N.invoke(N.id("self"), "g", [N.spread(N.id("args"))]))], { returns: N.tref("Self") })],
      { supers: [N.tref("TypedDict")], params: [N.param("total", undefined, { value: N.bool(false) })], doc: N.doc("A box.") },
    );
    expect(p.print(cls)).toBe('class Box(TypedDict, total=False):\n    """A box."""\n\n    def f(self, *args: int) -> Self:\n        return self.g(*args)');
  });

  test("a constant's docstring follows it", () => {
    expect(p.print(N.const("X", N.int(1), { type: N.tref("Final"), doc: N.doc("The x.") }))).toBe('X: Final = 1\n"""The x."""');
  });

  test("a long Literal breaks at a narrow width", () => {
    const narrow = new (class extends PythonPrinter {
      override width = 30;
    })();
    const lit = N.typeAlias("Fmt", N.tref("Literal", ["jpeg", "png", "webp", "avif"].map((v) => N.tlit(N.str(v)))));
    expect(p.print(lit)).toBe('Fmt = Literal["jpeg", "png", "webp", "avif"]');
    expect(narrow.print(lit)).toBe('Fmt = Literal[\n    "jpeg",\n    "png",\n    "webp",\n    "avif",\n]');
  });

  test("if/else and imports", () => {
    const backport = N.if(N.binary(">=", N.get(N.id("sys"), "version_info"), N.tuple([N.int(3), N.int(11)])), [N.imp("typing", ["Self"])], [N.imp("typing_extensions", ["Self"])]);
    expect(p.print(backport)).toBe("if sys.version_info >= (3, 11):\n    from typing import Self\nelse:\n    from typing_extensions import Self");
    expect(p.print(N.imp("sys"))).toBe("import sys");
  });
});
