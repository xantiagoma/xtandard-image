# Credo, run with --strict. The generated files (task clients:generate) are
# excluded; Dialyzer still checks them.
%{
  configs: [
    %{
      name: "default",
      strict: true,
      files: %{
        included: ["lib/", "test/", "scripts/", "mix.exs"],
        excluded: [~r"/_build/", ~r"/deps/", ~r"lib/xtandard_image/generated/"]
      },
      checks: %{
        extra: [
          {Credo.Check.Readability.Specs, []},
          {Credo.Check.Readability.StrictModuleLayout, []},
          # Test modules are described by a comment, not a @moduledoc.
          {Credo.Check.Readability.ModuleDoc, [files: %{excluded: ["test/"]}]}
        ]
      }
    }
  ]
}
