defmodule XtandardImage.Signing do
  @moduledoc """
  URL signing, byte-compatible with the server (`internal/security/sign`).

    * imgproxy: `base64url(HMAC-SHA256(key, salt <> path)[0, size])`, the
      signature segment of `/{signature}/{options}/{source}`; `/video` URLs
      sign `"/video" <> path`.
    * `xi_sig`: the same construction over `path <> "?" <> query-without-xi_sig`,
      appended as the last query parameter.

  Examples use imgproxy's documented key (`secret`) and salt (`hello`):

      iex> key = XtandardImage.SigningKey.new!("736563726574", "68656C6C6F")
      iex> XtandardImage.Signing.sign("/rs:fill:300:400:0/g:sm/aHR0cDovL2V4YW1w/bGUuY29tL2ltYWdl/cy9jdXJpb3NpdHku/anBn.png", key)
      "oKfUtW34Dvo2BGQehJFR4Nr0_rIjOtdtzJ3QFsUcXH8"
  """

  alias XtandardImage.Error
  alias XtandardImage.SigningKey
  alias XtandardImage.Spec
  alias XtandardImage.Text

  @xi_sig Spec.xi_sig_param()

  @doc """
  Signs a message with the salted HMAC-SHA256 of the imgproxy and `xi_sig`
  schemes. Like the server, a message without a leading `/` is signed as if it
  had one.
  """
  @spec sign(String.t(), SigningKey.t()) :: String.t()
  def sign(message, %SigningKey{key: key, salt: salt, size: size}) when is_binary(message) do
    :crypto.mac(:hmac, :sha256, key, salt <> slash(message))
    |> binary_part(0, size)
    |> Text.b64url()
  end

  @doc """
  Returns `"/" <> signature <> path` for an imgproxy path (everything after the
  signature segment).
  """
  @spec sign_imgproxy_path(String.t(), SigningKey.t()) :: String.t()
  def sign_imgproxy_path(path, %SigningKey{} = key) when is_binary(path) do
    p = slash(path)
    "/" <> sign(p, key) <> p
  end

  @doc """
  Splits a raw query into its `xi_sig` value and the message `xi_sig` signs:
  the path, then `?` and the remaining non-empty parameters in their original
  order and encoding. Two `xi_sig` parameters are an `:invalid_request` (the
  server answers 403).

      iex> XtandardImage.Signing.xi_message("/", "url=x&&xi_sig=s&w=1")
      {:ok, {"s", "/?url=x&w=1"}}
  """
  @spec xi_message(String.t(), String.t()) ::
          {:ok, {String.t() | nil, String.t()}} | {:error, Error.t()}
  def xi_message(path, raw_query), do: Error.wrap(fn -> xi_message!(path, raw_query) end)

  @doc "Like `xi_message/2`, raising `XtandardImage.Error`."
  @spec xi_message!(String.t(), String.t()) :: {String.t() | nil, String.t()}
  def xi_message!(path, raw_query) when is_binary(path) and is_binary(raw_query) do
    {sig, kept} =
      raw_query
      |> String.split("&")
      |> Enum.reject(&(&1 == ""))
      |> Enum.reduce({nil, []}, &take_signature!/2)

    message = if kept == [], do: path, else: path <> "?" <> Enum.join(Enum.reverse(kept), "&")
    {sig, message}
  end

  defp take_signature!(part, {sig, kept}) do
    case String.split(part, "=", parts: 2) do
      [@xi_sig | _] when sig != nil ->
        Error.raise!(:invalid_request, "query has more than one #{@xi_sig} parameter")

      [@xi_sig | value] ->
        {List.first(value, ""), kept}

      _ ->
        {sig, [part | kept]}
    end
  end

  @doc """
  Returns `raw_query` without any `xi_sig` and with a fresh `xi_sig` for
  `path` appended last. `path` is the escaped path the dialect sees, after its
  mount prefix.

      iex> key = XtandardImage.SigningKey.new!("736563726574", "68656C6C6F")
      iex> {:ok, q} = XtandardImage.Signing.sign_xi_query("/", "url=x&xi_sig=old&w=1", key)
      iex> String.starts_with?(q, "url=x&w=1&xi_sig=")
      true
  """
  @spec sign_xi_query(String.t(), String.t(), SigningKey.t()) ::
          {:ok, String.t()} | {:error, Error.t()}
  def sign_xi_query(path, raw_query, key),
    do: Error.wrap(fn -> sign_xi_query!(path, raw_query, key) end)

  @doc "Like `sign_xi_query/3`, raising `XtandardImage.Error`."
  @spec sign_xi_query!(String.t(), String.t(), SigningKey.t()) :: String.t()
  def sign_xi_query!(path, raw_query, %SigningKey{} = key) do
    raw = String.replace_prefix(raw_query, "?", "")
    {_, msg} = xi_message!(path, raw)
    sig = sign(msg, key)
    q = binary_part(msg, byte_size(path), byte_size(msg) - byte_size(path))

    case q do
      "" -> "#{@xi_sig}=#{sig}"
      "?" <> rest -> "#{rest}&#{@xi_sig}=#{sig}"
    end
  end

  @doc """
  Signs a URL (absolute, or a path with an optional query) with `xi_sig`.

  `:mount` is the dialect's mount prefix, which the signed path excludes
  (`"/q"` for the query dialect). A fragment is dropped. A path outside the
  mount, or a query with two `xi_sig`, is an `:invalid_request`.

      iex> key = XtandardImage.SigningKey.new!("736563726574", "68656C6C6F")
      iex> {:ok, url} = XtandardImage.Signing.sign_xi_url("https://img.example.com/q/?url=x", key, mount: "/q")
      iex> String.starts_with?(url, "https://img.example.com/q/?url=x&xi_sig=")
      true
  """
  @spec sign_xi_url(String.t(), SigningKey.t(), keyword()) ::
          {:ok, String.t()} | {:error, Error.t()}
  def sign_xi_url(url, key, opts \\ []), do: Error.wrap(fn -> sign_xi_url!(url, key, opts) end)

  @doc "Like `sign_xi_url/3`, raising `XtandardImage.Error`."
  @spec sign_xi_url!(String.t(), SigningKey.t(), keyword()) :: String.t()
  def sign_xi_url!(url, %SigningKey{} = key, opts \\ []) when is_binary(url) do
    [no_hash | _] = String.split(url, "#", parts: 2)

    {head, query} =
      case String.split(no_hash, "?", parts: 2) do
        [h] -> {h, ""}
        [h, q] -> {h, q}
      end

    {origin, path} = split_origin(head)
    mount = opts |> Keyword.get(:mount, "") |> String.trim_trailing("/")
    path = strip_mount(path, mount)
    origin <> mount <> path <> "?" <> sign_xi_query!(path, query, key)
  end

  defp split_origin(head) do
    if Regex.match?(~r/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//, head) do
      [scheme, rest] = String.split(head, "//", parts: 2)

      case String.split(rest, "/", parts: 2) do
        [authority] -> {scheme <> "//" <> authority, "/"}
        [authority, path] -> {scheme <> "//" <> authority, "/" <> path}
      end
    else
      {"", head}
    end
  end

  defp strip_mount(path, ""), do: path

  defp strip_mount(path, mount) do
    cond do
      path == mount -> "/"
      String.starts_with?(path, mount <> "/") -> String.replace_prefix(path, mount, "")
      true -> Error.raise!(:invalid_request, "path #{path} is not under the mount #{mount}")
    end
  end

  defp slash("/" <> _ = s), do: s
  defp slash(s), do: "/" <> s
end
