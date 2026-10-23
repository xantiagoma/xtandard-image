// C#'s leaf tokens and printer (WP-146).
//
//   bun test clients/codegen/test/csharp.test.ts

import { describe, expect, test } from "bun:test";
import { ARROW_NEXT_LINE, camelName, CSharpPrinter, ctor, escape, init, member, nextLine, xdoc, xml } from "../lang/csharp.ts";
import { N } from "../model.ts";

const p = new CSharpPrinter();

describe("csharp leaf tokens", () => {
  test("identifiers: casing, leading digits and @-escaped keywords", () => {
    expect(member("min-width")).toBe("MinWidth");
    expect(member("c_obj")).toBe("CObj");
    expect(member("3d")).toBe("_3d");
    expect(camelName("x_offset")).toBe("xOffset");
    expect(escape(camelName("class"))).toBe("@class");
    expect(escape("width")).toBe("width");
  });

  test("strings escape quotes, backslashes and control characters", () => {
    expect(p.print(N.str('a"b\\c\nd\t'))).toBe('"a\\"b\\\\c\\nd\\t"');
    expect(p.print(N.str("{x} ñ"))).toBe('"{x} ñ"');
  });

  test("numbers and literals", () => {
    expect(p.print(N.list([N.int(300), N.num(0.5), N.bool(true), N.null()]))).toBe("[300, 0.5, true, null]");
  });

  test("XML doc text is escaped and `code` becomes <c>", () => {
    expect(xml("a < b & `c` > d")).toBe("a &lt; b &amp; <c>c</c> &gt; d");
    expect(p.print(xdoc("param", "x < 1", [["name", "x"]]))).toBe('/// <param name="x">x &lt; 1</param>');
    expect(p.print(xdoc("summary", "one\n\ntwo"))).toBe("/// <summary>\n/// one\n///\n/// two\n/// </summary>");
    expect(p.print(xdoc("inheritdoc", undefined))).toBe("/// <inheritdoc/>");
  });
});

describe("csharp printer", () => {
  test("a property with an expanded dictionary initializer on the next line", () => {
    const dict = init(N.new(N.tref("Dictionary", [N.tref("string"), N.tref("int")]), []), [N.entry(N.str("a"), N.int(1))], "expanded");
    const f = N.field("Codes", N.tref("IReadOnlyDictionary", [N.tref("string"), N.tref("int")]), { mods: ["public", "static", "get"], value: nextLine(dict) });
    expect(p.print(f)).toBe('public static IReadOnlyDictionary<string, int> Codes { get; } =\n    new Dictionary<string, int>()\n    {\n        ["a"] = 1,\n    };');
  });

  test("expression bodies, constructors and loops", () => {
    const m = N.func("Q", [N.param("v", N.tref("long"))], N.call(N.id("Option"), [N.str("q"), N.list([N.id("v")])]), {
      mods: ["public", ARROW_NEXT_LINE],
      returns: N.tref("TSelf"),
    });
    expect(p.print(m)).toBe('public TSelf Q(long v)\n    => Option("q", [v]);');
    expect(p.print(ctor(N.func("C", [], [], { mods: ["public"] }), [N.str("c")]))).toBe('public C()\n    : base("c")\n{\n}');
    const loop = N.forEach("e", N.id("xs"), [N.stmt(N.invoke(N.id("args"), "Add", [N.id("e")]))]);
    expect(p.print(loop)).toBe("foreach (var e in xs)\n{\n    args.Add(e);\n}");
  });

  test("a long collection expression breaks at a narrow width", () => {
    const narrow = new (class extends CSharpPrinter {
      override width = 20;
    })();
    const list = N.list(["jpeg", "png", "webp"].map(N.str));
    expect(p.print(list)).toBe('["jpeg", "png", "webp"]');
    expect(narrow.print(list)).toBe('[\n    "jpeg",\n    "png",\n    "webp",\n]');
  });
});
