// Elixir's leaf tokens and printer (WP-146).
//
//   bun test clients/codegen/test/elixir.test.ts

import { describe, expect, test } from "bun:test";
import { atom, ElixirPrinter, ident, string, X } from "../lang/elixir.ts";
import { N } from "../model.ts";

const p = new ElixirPrinter();

describe("elixir leaf tokens", () => {
  test("strings escape quotes, backslashes, control characters and interpolation", () => {
    expect(string('a"b\\c\n')).toBe('"a\\"b\\\\c\\n"');
    expect(string("#{x} # {y}")).toBe('"\\#{x} # {y}"');
    expect(p.print(N.str("ñ ≥"))).toBe('"ñ ≥"');
  });

  test("atoms are bare when they are plain names, quoted otherwise", () => {
    expect(atom("fill")).toBe(":fill");
    expect(atom("_x9")).toBe(":_x9");
    expect(atom("fill-down")).toBe(':"fill-down"');
    expect(atom("Up")).toBe(':"Up"');
    expect(atom("#{a}")).toBe(':"\\#{a}"');
  });

  test("function names: dashes, digits, case and reserved words", () => {
    expect(ident("min-width")).toBe("min_width");
    expect(ident("3d")).toBe("o_3d");
    expect(ident("RS")).toBe("rs");
    expect(ident("do")).toBe("do_");
    expect(ident("end")).toBe("end_");
  });

  test("literals and numbers", () => {
    expect(p.print(N.list([N.null(), N.bool(true), N.int(300), N.num(0.5), N.sym("a")]))).toBe("[nil, true, 300, 0.5, :a]");
    expect(p.print(N.tuple([N.str("a"), N.int(1)]))).toBe('{"a", 1}');
    expect(p.print(N.map([N.entry(N.str("k"), N.int(1))]))).toBe('%{"k" => 1}');
  });
});

describe("elixir printer", () => {
  test("typespecs: remote types, unions with nil last, tuples, lists and maps", () => {
    const u = X.union([N.tunion([X.atom("a"), X.nil]), X.t("String.t"), X.atom("a")]);
    expect(p.print(u)).toBe(":a | String.t() | nil");
    expect(p.print(N.ttuple([X.t("integer"), X.tlist(X.t("number"))]))).toBe("{integer(), [number()]}");
    expect(p.print(X.tmap(X.t("optional", X.t("String.t")), X.t("term")))).toBe("%{optional(String.t()) => term()}");
  });

  test("an expanded @type puts each member on its own line after |", () => {
    const m = N.namespace("M", [N.typeAlias("v", N.tunion([N.ttuple([X.t("integer")]), X.t("integer")]), { mods: ["expanded"] })]);
    expect(p.print(m)).toBe("defmodule M do\n  @type v ::\n          {integer()}\n          | integer()\nend");
  });

  test("defs: one-liners, guards, block bodies and delegates", () => {
    expect(p.print(X.def("v", [], N.int(1)))).toBe("def v, do: 1");
    expect(p.print(X.def("one", [N.id("v")], N.null(), N.call(N.id("is_map"), [N.id("v")])))).toBe("def one(v) when is_map(v), do: nil");
    expect(p.print(X.def("f", [N.id("m")], [N.stmt(N.new(N.tref("__MODULE__"), [N.named("a", N.int(1))], { layout: "expanded" }))]))).toBe(
      "def f(m) do\n  %__MODULE__{\n    a: 1\n  }\nend",
    );
    expect(p.print(X.defdelegate("build!", ["chain"], "Chain"))).toBe("defdelegate build!(chain), to: Chain");
    expect(p.print(N.invoke(N.id("Enum"), "map", [N.id("v"), X.capture(N.call(N.id("one"), [X.arg(1)]))]))).toBe("Enum.map(v, &one(&1))");
  });

  test("a heredoc keeps its lines at the attribute's indentation", () => {
    const m = N.namespace("M", [X.attr("moduledoc", X.heredoc("Title.\n\n    iex> 1\n"))]);
    expect(p.print(m)).toBe('defmodule M do\n  @moduledoc ~S"""\n  Title.\n\n      iex> 1\n  """\nend');
  });

  test("a long map breaks one entry per line at a narrow width, without a trailing comma", () => {
    const narrow = new (class extends ElixirPrinter {
      override width = 20;
    })();
    const m = N.map(["alpha", "beta", "gamma"].map((k) => N.entry(N.str(k), N.int(1))));
    expect(p.print(m)).toBe('%{"alpha" => 1, "beta" => 1, "gamma" => 1}');
    expect(narrow.print(m)).toBe('%{\n  "alpha" => 1,\n  "beta" => 1,\n  "gamma" => 1\n}');
  });
});
