# Checks the licences of the package's runtime dependency closure against the
# ADR 0006 allowlist (ADR 0013): MIT, BSD, Apache-2.0, ISC.
#
#   mix run --no-start --no-compile scripts/check_licenses.exs
#
# The roots are the deps of mix.exs without `only:` (Req, which is optional:
# what a project gets when it adds Req for XtandardImage.Client); the closure
# follows each Hex package's non-optional requirements (deps/*/hex_metadata.config).
# There are no exceptions; one would be listed in @exceptions with its reason
# and in the README.

defmodule XtandardImage.LicenseCheck do
  @moduledoc false

  @allowed ~r/^(MIT|MIT-0|BSD|BSD-[23]-Clause|Apache-2\.0|Apache Software License|ISC)$/i
  # Package name => {licence pattern, reason}.
  @exceptions %{}

  @spec run() :: non_neg_integer()
  def run do
    roots =
      for dep <- Mix.Project.config()[:deps],
          opts = if(is_tuple(dep) and tuple_size(dep) == 3, do: elem(dep, 2), else: []),
          not Keyword.has_key?(opts, :only),
          do: elem(dep, 0) |> Atom.to_string()

    roots
    |> closure(%{})
    |> Enum.sort()
    |> Enum.count(fn {name, {version, licences}} -> not report(name, version, licences) end)
  end

  defp closure([], seen), do: seen

  defp closure([name | rest], seen) when is_map_key(seen, name), do: closure(rest, seen)

  defp closure([name | rest], seen) do
    meta = metadata(name)
    requirements = for r <- meta["requirements"] || [], not r["optional"], do: r["name"]
    closure(requirements ++ rest, Map.put(seen, name, {meta["version"], meta["licenses"] || []}))
  end

  defp metadata(name) do
    path = Path.join([Mix.Project.deps_path(), name, "hex_metadata.config"])
    {:ok, terms} = :file.consult(String.to_charlist(path))
    Map.new(terms, fn {k, v} -> {k, normalise(v)} end)
  end

  # Requirements are a list of proplists (newer Hex) or a map of name => proplist.
  defp normalise(v) when is_map(v),
    do: Enum.map(v, fn {name, props} -> Map.put(Map.new(props), "name", name) end)

  defp normalise([[{_, _} | _] | _] = v), do: Enum.map(v, &Map.new/1)
  defp normalise(v), do: v

  defp report(name, version, licences) do
    status =
      cond do
        licences != [] and Enum.all?(licences, &Regex.match?(@allowed, &1)) -> "ok"
        exception?(name, licences) -> "exception"
        true -> "NOT ALLOWED"
      end

    IO.puts("#{String.pad_trailing(status, 12)} #{name} #{version}: #{Enum.join(licences, " OR ")}")
    status != "NOT ALLOWED"
  end

  defp exception?(name, licences) do
    case @exceptions[name] do
      {pattern, _reason} -> Enum.any?(licences, &Regex.match?(pattern, &1))
      nil -> false
    end
  end
end

case XtandardImage.LicenseCheck.run() do
  0 -> :ok
  bad -> Mix.raise("#{bad} runtime dependencies have a licence outside the allowlist")
end
