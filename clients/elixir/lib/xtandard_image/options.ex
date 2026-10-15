defmodule XtandardImage.Options do
  @moduledoc false
  # Option catalogues at run time and their serialisation into option
  # segments. An entry is one option occurrence: {name, [arg]}, the name as
  # given (canonical or alias).

  alias XtandardImage.Error
  alias XtandardImage.Generated.Catalogue
  alias XtandardImage.Text

  @type catalogue :: :processing | :info | :video
  @type style :: :short | :canonical | :given
  @type arg :: String.t() | number() | boolean() | nil
  @type entry :: {String.t(), [arg()]}
  @type pipeline :: [entry()]

  @doc "Converts one options keyword list or map into entries, in order; `nil` values are skipped."
  @spec entries_of!(term()) :: pipeline()
  def entries_of!(options) when is_map(options), do: options |> Map.to_list() |> entries_of!()

  def entries_of!(options) when is_list(options) do
    options |> Enum.map(&entry!/1) |> Enum.reject(&is_nil/1)
  end

  def entries_of!(other),
    do:
      Error.raise!(
        :invalid_argument,
        "options must be a keyword list or a map, got #{inspect(other)}"
      )

  defp entry!({_name, nil}), do: nil
  defp entry!({name, value}), do: {name!(name), args!(name, value)}

  defp entry!(other),
    do:
      Error.raise!(
        :invalid_argument,
        "an option is a {name, arguments} pair, got #{inspect(other)}"
      )

  @doc "The name of an option as a string."
  @spec name!(term()) :: String.t()
  def name!(name) when is_binary(name), do: name
  def name!(name) when is_atom(name) and not is_nil(name), do: Atom.to_string(name)
  def name!(name), do: Error.raise!(:unknown_option, "invalid option name #{inspect(name)}")

  @doc "The argument list of one option value: a list, a tuple or a bare first argument."
  @spec args!(term(), term()) :: [arg()]
  def args!(name, value) when is_list(value), do: Enum.map(value, &arg!(name, &1))
  def args!(name, value) when is_tuple(value), do: args!(name, Tuple.to_list(value))
  def args!(name, value), do: [arg!(name, value)]

  defp arg!(_name, a) when is_binary(a) or is_number(a) or is_boolean(a) or is_nil(a), do: a
  defp arg!(_name, a) when is_atom(a), do: Atom.to_string(a)

  defp arg!(name, a),
    do: Error.raise!(:invalid_argument, "option #{name!(name)}: unsupported argument #{inspect(a)}")

  @doc "One entry list per pipeline: a keyword list or map is one pipeline, a list of them chains."
  @spec pipelines_of!(term()) :: [pipeline()]
  def pipelines_of!(nil), do: [[]]
  def pipelines_of!([]), do: [[]]
  def pipelines_of!(options) when is_map(options), do: [entries_of!(options)]

  def pipelines_of!([first | _] = options) when is_list(first) or is_map(first),
    do: Enum.map(options, &entries_of!/1)

  def pipelines_of!(options), do: [entries_of!(options)]

  @doc "Formats one argument. Strings are checked, never escaped: the server does not unescape them."
  @spec format_arg!(arg(), String.t(), String.t()) :: String.t()
  def format_arg!(nil, _sep, _where), do: ""
  def format_arg!(true, _sep, _where), do: "1"
  def format_arg!(false, _sep, _where), do: "0"
  def format_arg!(n, _sep, _where) when is_number(n), do: Text.format_number(n)

  def format_arg!(s, sep, where) when is_binary(s) do
    if String.contains?(s, sep) or not String.valid?(s) or
         Regex.match?(~r/[\/?#\s\x00-\x1f\x7f\x{FEFF}]/u, s) do
      Error.raise!(
        :invalid_argument,
        "#{where}: argument #{inspect(s)} contains a character that cannot appear in an option"
      )
    end

    s
  end

  @doc "The name an option is written with; `:unknown_option` for a name not in the catalogue."
  @spec written_name!(catalogue(), String.t(), style()) :: String.t()
  def written_name!(cat, name, style) do
    case Catalogue.names(cat) do
      %{^name => canonical} ->
        case style do
          :given -> name
          :canonical -> canonical
          :short -> Map.get(Catalogue.short(cat), canonical, canonical)
        end

      _ ->
        Error.raise!(:unknown_option, "unknown #{label(cat)} option #{inspect(name)}")
    end
  end

  @doc "The canonical name of an option, or nil."
  @spec canonical(catalogue(), String.t()) :: String.t() | nil
  def canonical(cat, name), do: Map.get(Catalogue.names(cat), name)

  @doc "Formats one option segment (`rs:fill:300:400`)."
  @spec format_entry!(catalogue(), entry(), String.t(), style()) :: String.t()
  def format_entry!(cat, {name, args}, sep, style) do
    written = written_name!(cat, name, style)

    if args == [],
      do: Error.raise!(:invalid_argument, "option #{name}: at least one argument is required")

    formatted =
      args
      |> Enum.with_index(1)
      |> Enum.map(fn {a, i} -> format_arg!(a, sep, "option #{name} argument #{i}") end)

    Enum.join([written | formatted], sep)
  end

  @doc "Normalises an option name style."
  @spec style!(term()) :: style()
  def style!(style) when style in [:short, :canonical, :given], do: style

  def style!(style) when style in ["short", "canonical", "given"],
    do: String.to_existing_atom(style)

  def style!(style),
    do:
      Error.raise!(
        :invalid_request,
        "option_names must be :short, :canonical or :given, got #{inspect(style)}"
      )

  defp label(:processing), do: "imgproxy"
  defp label(kind), do: Atom.to_string(kind)
end
