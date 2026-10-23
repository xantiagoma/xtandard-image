// PHP's leaf tokens and printer (WP-146).
//
//   bun test clients/codegen/test/php.test.ts

import { describe, expect, test } from "bun:test";
import { reservedMethods } from "../emitters/php.ts";
import { camel, caseName, docText, P, PhpPrinter, quote } from "../lang/php.ts";
import { N } from "../model.ts";

const p = new PhpPrinter();

describe("php leaf tokens", () => {
  test("single-quoted strings escape quotes and backslashes; $ and newlines stay literal", () => {
    expect(quote("it's")).toBe("'it\\'s'");
    expect(quote("a\\b")).toBe("'a\\\\b'");
    expect(p.print(N.str("$x {y}"))).toBe("'$x {y}'");
    expect(p.print(N.str("\\'"))).toBe("'\\\\\\''");
  });

  test("variables carry their sigil; members use -> and ::", () => {
    expect(p.print(N.invoke(N.id("this"), "option", [N.str("q"), N.spread(N.id("args"))]))).toBe("$this->option('q', ...$args)");
    expect(p.print(N.get(P.cls("Constants"), "ERROR_STATUS", { mods: ["static"] }))).toBe("Constants::ERROR_STATUS");
  });

  test("identifier casing", () => {
    expect(camel("min-width")).toBe("minWidth");
    expect(camel("crop_AR")).toBe("cropAr");
    expect(caseName("fill-down")).toBe("FillDown");
    expect(caseName("3d")).toBe("V3d");
    expect(reservedMethods.has("withpdfpassword")).toBe(true);
    expect(reservedMethods.has("__tostring")).toBe(true);
  });

  test("numbers, booleans and null", () => {
    expect(p.print(N.list([N.int(300), N.num(0.5), N.num(1e21), N.bool(true), N.null()]))).toBe("[300, 0.5, 1e+21, true, null]");
  });

  test("docblocks escape the comment terminator", () => {
    expect(docText("a */ b")).toBe("a *\\/ b");
    expect(p.print(N.doc("x */\n\ny"))).toBe("/**\n * x *\\/\n *\n * y\n */");
    expect(p.print(N.doc("one", { mods: ["inline"] }))).toBe("/** one */");
  });

  test("docblock tags print their types", () => {
    const shape = P.shape([{ key: N.str("k"), optional: true, type: N.tunion([N.tref("int"), N.tref("null")]) }]);
    const block = P.docblock(["Types.", "", P.tag("phpstan-type", shape, { name: "T" }), P.tag("param", N.tref("int"), { name: "args", variadic: true })]);
    expect(p.print(block)).toBe("/**\n * Types.\n *\n * @phpstan-type T array{'k'?: int|null}\n * @param int ...$args\n */");
  });
});

describe("php printer", () => {
  test("a final class with a constant table and a private constructor", () => {
    const cls = N.type(
      "class",
      "Names",
      [N.const("A", N.map([N.entry(N.str("a"), N.str("b"))], { layout: "expanded" }), { mods: ["public"] }), N.blank(), N.func("__construct", [], [], { mods: ["private"] })],
      { mods: ["final"] },
    );
    expect(p.print(cls)).toBe("final class Names\n{\n    public const A = [\n        'a' => 'b',\n    ];\n\n    private function __construct()\n    {\n    }\n}");
  });

  test("an abstract method ends with a semicolon; a method body is braced on its own lines", () => {
    const abs = N.func("option", [N.param("args", N.tunion([N.tref("string"), N.tref("null")]), { mods: ["variadic"] })], undefined, {
      returns: N.tref("static"),
      mods: ["abstract", "public"],
    });
    expect(p.print(abs)).toBe("abstract public function option(string|null ...$args): static;");
    const m = N.func("q", [], [N.ret(N.int(1))], { returns: N.tref("int"), mods: ["public"] });
    expect(p.print(m)).toBe("public function q(): int\n{\n    return 1;\n}");
  });

  test("a backed enum", () => {
    expect(p.print(N.enum("Fmt", [N.member("Png", N.str("png"))], { type: N.tref("string") }))).toBe("enum Fmt: string\n{\n    case Png = 'png';\n}");
  });

  test("a long array breaks at a narrow width", () => {
    const narrow = new (class extends PhpPrinter {
      override width = 20;
    })();
    const list = N.list(["jpeg", "png", "webp", "avif"].map(N.str));
    expect(p.print(list)).toBe("['jpeg', 'png', 'webp', 'avif']");
    expect(narrow.print(list)).toBe("[\n    'jpeg',\n    'png',\n    'webp',\n    'avif',\n]");
  });
});
