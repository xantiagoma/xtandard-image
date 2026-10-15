defmodule XtandardImage.Sources do
  @moduledoc """
  Source references: the string a URL carries as its source. These helpers
  only join strings; the URL builders escape or encode the result.

      https://host/path         an http source
      s3://bucket/key           gs://bucket/key   abs://container/key
      local:///path             ftp://host/path   sftp://host/path
      xi://<source>/<key>       a named source (XI_SOURCES)
  """

  alias XtandardImage.Error
  alias XtandardImage.Text

  @doc """
  A named source reference, `xi://<name>/<key>`. The key may be relative or,
  for an http source, an absolute URL. A name that does not match
  `XtandardImage.Spec.xi_name_pattern/0` is an `:invalid_name`.

      iex> XtandardImage.Sources.xi_source("photos", "2026/cat.jpg")
      {:ok, "xi://photos/2026/cat.jpg"}
      iex> XtandardImage.Sources.xi_source!("web", "https://cdn.example.com/a.jpg")
      "xi://web/https://cdn.example.com/a.jpg"
  """
  @spec xi_source(String.t(), String.t()) :: {:ok, String.t()} | {:error, Error.t()}
  def xi_source(name, key), do: Error.wrap(fn -> xi_source!(name, key) end)

  @doc "Like `xi_source/2`, raising `XtandardImage.Error`."
  @spec xi_source!(String.t(), String.t()) :: String.t()
  def xi_source!(name, key) when is_binary(name) and is_binary(key) do
    if not source_name?(name),
      do: Error.raise!(:invalid_name, "invalid source name #{inspect(name)}")

    "xi://" <> name <> "/" <> String.trim_leading(key, "/")
  end

  # ^[A-Za-z0-9][A-Za-z0-9_.-]*$ (XtandardImage.Spec.xi_name_pattern/0)
  defp source_name?(<<c, rest::binary>>) when c in ?a..?z or c in ?A..?Z or c in ?0..?9,
    do: name_rest?(rest)

  defp source_name?(_), do: false

  defp name_rest?(<<c, rest::binary>>)
       when c in ?a..?z or c in ?A..?Z or c in ?0..?9 or c in [?_, ?., ?-],
       do: name_rest?(rest)

  defp name_rest?(<<>>), do: true
  defp name_rest?(_), do: false

  @doc """
  A source reference for a scheme: the bucket, container, host or source name,
  then the key. `local` takes only a path (`local:///<path>`).

  A missing authority, one with `/`, `?`, `#` or whitespace, or one given for
  `local` is an `:invalid_name`.

      iex> XtandardImage.Sources.source_url(:s3, "photos", "cat.jpg")
      {:ok, "s3://photos/cat.jpg"}
      iex> XtandardImage.Sources.source_url!("local", "", "img/cat.jpg")
      "local:///img/cat.jpg"
  """
  @spec source_url(XtandardImage.Spec.source_scheme(), String.t(), String.t()) ::
          {:ok, String.t()} | {:error, Error.t()}
  def source_url(scheme, authority, key),
    do: Error.wrap(fn -> source_url!(scheme, authority, key) end)

  @doc "Like `source_url/3`, raising `XtandardImage.Error`."
  @spec source_url!(XtandardImage.Spec.source_scheme(), String.t(), String.t()) :: String.t()
  def source_url!(scheme, authority, key) when is_atom(scheme),
    do: source_url!(Atom.to_string(scheme), authority, key)

  def source_url!("xi", authority, key), do: xi_source!(authority, key)

  def source_url!("local", authority, key) when is_binary(key) do
    if authority != "",
      do: Error.raise!(:invalid_name, "local sources have no authority: local:///<path>")

    "local:///" <> String.trim_leading(key, "/")
  end

  def source_url!(scheme, authority, key)
      when is_binary(scheme) and is_binary(authority) and is_binary(key) do
    if authority == "" or String.match?(authority, ~r/[\/?#\s]/u),
      do: Error.raise!(:invalid_name, "invalid #{scheme} authority #{inspect(authority)}")

    scheme <> "://" <> authority <> "/" <> String.trim_leading(key, "/")
  end

  @doc """
  Percent-escapes a source for a `plain/` URL; the server path-unescapes it.
  ASCII letters, digits and `XtandardImage.Spec.plain_safe/0` are kept;
  everything else is `%XX` per UTF-8 byte.

      iex> XtandardImage.Sources.escape_plain_source("https://e.com/a b@c%d?e#f;g=h")
      "https://e.com/a%20b%40c%25d%3Fe%23f;g=h"
  """
  @spec escape_plain_source(String.t()) :: String.t()
  defdelegate escape_plain_source(source), to: Text

  @doc """
  Percent-escapes a query key or value, keeping `:`, `/` and `,` readable
  (`XtandardImage.Spec.query_safe/0`).

      iex> XtandardImage.Sources.escape_query_value("a&b=c+d e/f:g")
      "a%26b%3Dc%2Bd%20e/f:g"
  """
  @spec escape_query_value(String.t()) :: String.t()
  defdelegate escape_query_value(value), to: Text
end
