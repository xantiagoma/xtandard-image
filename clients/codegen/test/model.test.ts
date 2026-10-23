// The code model, the neutral model builder and the printer base (WP-146).
//
//   bun test clients/codegen/test/model.test.ts

import { describe, expect, test } from "bun:test";
import { catalogue, catalogues, enums, imaginaryParams, optionTags, responseSchemas } from "../build.ts";
import { N, union } from "../model.ts";
import { Printer } from "../printer.ts";
import { miniSpec } from "./fixture.ts";

const spec = await miniSpec();
const p = new Printer();

describe("model", () => {
  test("constructors leave optional fields absent", () => {
    expect(N.const("A", N.int(1))).toEqual({ k: "const", name: "A", value: { k: "lit", type: "int", value: 1 }, mods: [], annotations: [] });
    expect("type" in N.param("x")).toBe(false);
    expect(N.if(N.bool(true), [])).not.toHaveProperty("else");
  });

  test("union keeps distinct members in order and a nested union as one member", () => {
    const a = N.tref("A");
    const b = N.tref("B");
    expect(union([a, b, a])).toEqual(N.tunion([a, b]));
    expect(union([a, a])).toEqual(a);
    expect(union([N.tunion([a, b]), a])).toEqual(N.tunion([N.tunion([a, b]), a]));
    expect(union([N.tunion([a, b]), a], { flatten: true })).toEqual(N.tunion([a, b]));
  });

  test("ext nodes carry their language kind", () => {
    const e = N.ext("py.ellipsis", {});
    expect(e.k).toBe("py.ellipsis");
    expect(() => p.print(e)).toThrow("no py.ellipsis nodes");
  });
});

describe("neutral model", () => {
  test("catalogues hold every option with names, shapes and tags", () => {
    const [processing, info, video] = catalogues(spec);
    expect([processing!.kind, info!.kind, video!.kind]).toEqual(["processing", "info", "video"]);
    const crop = processing!.byName.get("crop")!;
    expect(crop.names).toEqual(["crop", ...crop.aliases]);
    expect(crop.shapes.length).toBeGreaterThan(1);
    expect(crop.shapes.some((s) => s.rest)).toBe(true);
    expect(processing!.names.map(([n]) => n)).toEqual([...processing!.names.map(([n]) => n)].sort());
    expect(catalogue(spec, "info").options.map((o) => o.name)).toEqual(["cache_tags", "size"]);
  });

  test("tags read like the clients' docs", () => {
    expect(optionTags({ ...spec.options.processing[0]!, milestone: "M1", scope: "pipeline", pro: true, security: false, removed: true })).toEqual([
      "milestone M1",
      "scope pipeline",
      "imgproxy Pro option (free here)",
      "removed: accepted and ignored",
    ]);
  });

  test("enums, imaginary parameters and schemas", () => {
    expect(enums(spec).image_format.values).toEqual(spec.media.image_formats);
    expect(imaginaryParams(spec)).not.toContain("url");
    const schemas = responseSchemas(spec);
    expect(schemas.map((s) => s.name)).toEqual(["ErrorBody", "ErrorResponse", "InfoDominant", "InfoObject", "InfoRGB"]);
    const props = schemas[3]!.properties.map(([n]) => n);
    expect(props).toEqual([...props].sort());
  });
});

describe("printer base", () => {
  test("expressions", () => {
    expect(p.print(N.invoke(N.id("a"), "b", [N.str('q"\\'), N.int(1), N.num(0.5), N.bool(false), N.null()]))).toBe('a.b("q\\"\\\\", 1, 0.5, false, null)');
    expect(p.print(N.list([N.int(1), N.int(2)]))).toBe("[1, 2]");
    expect(p.print(N.map([N.entry(N.str("k"), N.int(1))]))).toBe('{"k": 1}');
    expect(p.print(N.binary(">=", N.id("x"), N.int(3)))).toBe("x >= 3");
    expect(p.print(N.tref("Map", [N.tref("String"), N.tref("Int", [], { nullable: true })]))).toBe("Map<String, Int?>");
    expect(p.print(N.tunion([N.tref("A"), N.tlit(N.str("b"))]))).toBe('A | "b"');
  });

  test("numbers print as JavaScript writes them", () => {
    expect(p.print(N.num(1e21))).toBe("1e+21");
    expect(p.print(N.num(0.1))).toBe("0.1");
    expect(p.print(N.int(300))).toBe("300");
  });

  test("an expanded collection breaks; an auto one breaks only past the width", () => {
    const items = ["alpha", "beta", "gamma"].map(N.str);
    expect(p.print(N.list(items, { layout: "expanded" }))).toBe('[\n  "alpha",\n  "beta",\n  "gamma",\n]');
    const narrow = new (class extends Printer {
      override width = 12;
    })();
    expect(narrow.print(N.list(items))).toBe('[\n  "alpha",\n  "beta",\n  "gamma",\n]');
    expect(p.print(N.list(items))).toBe('["alpha", "beta", "gamma"]');
  });

  test("statements and blocks", () => {
    const sw = N.switch(N.id("k"), [N.case([N.str("a")], [N.ret(N.int(1))]), N.case([], [N.ret(N.int(0))])]);
    expect(p.print(sw)).toBe('switch k {\n  case "a":\n    return 1\n  default:\n    return 0\n}');
    expect(p.print(N.if(N.id("ok"), [N.stmt(N.call(N.id("f")))], [N.throw(N.id("e"))]))).toBe("if ok {\n  f()\n} else {\n  throw e\n}");
  });

  test("comments", () => {
    expect(p.print(N.comment("line", ["a", "", "b"]))).toBe("// a\n//\n// b");
    expect(p.print(N.doc("a\nb"))).toBe("/**\n * a\n * b\n */");
  });

  test("a file ends with a newline and blank() is an empty line", () => {
    expect(p.print(N.file([N.line("a"), N.blank(), N.line("b")]))).toBe("// a\n\n// b\n");
  });
});
