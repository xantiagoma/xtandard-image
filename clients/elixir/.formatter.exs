# lib/xtandard_image/generated/ is written by clients/codegen/emitters/elixir.ts
# (task clients:generate) and left out of the formatter.
[
  inputs:
    ["{mix,.formatter,.credo}.exs", "{config,scripts,test}/**/*.{ex,exs}", "lib/**/*.{ex,exs}"]
    |> Enum.flat_map(&Path.wildcard(&1, match_dot: true))
    |> Enum.reject(&String.starts_with?(&1, "lib/xtandard_image/generated/")),
  line_length: 100
]
