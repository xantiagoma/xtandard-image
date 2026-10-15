defmodule XtandardImage.Query do
  @moduledoc """
  Query dialect URLs, mounted at `/q/` (the server needs
  `XI_DIALECTS_QUERY_ENABLED=true`):

      {base_url}{mount}/?url=<source>&<native key>=…&o=<imgproxy option path>[&xi_sig=…]
      {base_url}{mount}/video?url=<source>&<video option>=<arg,arg>…[&xi_sig=…]
      {base_url}{mount}/<imaginary endpoint>?url=<source>&<param>=…[&xi_sig=…]

  With `:signing`, every URL ends in an `xi_sig` over the path after the mount
  and the query.

      iex> q = XtandardImage.query_urls()
      iex> XtandardImage.Query.url(q, "s3://b/cat.jpg", params: [w: 300, f: "webp"], options: [bl: 2])
      {:ok, "/q/?url=s3://b/cat.jpg&w=300&f=webp&o=bl:2"}

  Native keys (`:params`) are a keyword list, a map, or `{key, value}` pairs
  (keys may repeat); a `nil` value is skipped and booleans are written
  `true`/`false`.

  ## Configuration (`new/1`)

    * `:base_url` - the server origin (and any path before the mount).
    * `:mount` - the dialect's mount prefix (`XI_DIALECTS_QUERY_PREFIX`),
      default `"/q"`.
    * `:signing` - an `XtandardImage.SigningKey` (the imgproxy key and salt);
      without it URLs are unsigned.
    * `:encryption` - an `XtandardImage.EncryptionKey` for PDF passwords.
    * `:separator` - the argument separator inside `o=`, default `":"`.
    * `:option_names` - `:short` (default), `:canonical` or `:given`.
  """

  alias XtandardImage.Chain
  alias XtandardImage.EncryptionKey
  alias XtandardImage.Error
  alias XtandardImage.Imgproxy
  alias XtandardImage.Options
  alias XtandardImage.Signing
  alias XtandardImage.SigningKey
  alias XtandardImage.Spec
  alias XtandardImage.Text

  @type t :: %__MODULE__{
          base_url: String.t(),
          mount: String.t(),
          signing: SigningKey.t() | nil,
          encryption: EncryptionKey.t() | nil,
          separator: String.t(),
          option_names: :short | :canonical | :given
        }

  @typedoc "A native query value: booleans are written `true`/`false`, `nil` is skipped."
  @type value :: String.t() | atom() | number() | boolean() | nil

  @typedoc "Native keys or imaginary parameters, in order."
  @type params ::
          [{Spec.query_key() | Spec.imaginary_param(), value()}]
          | %{optional(atom() | String.t()) => value()}

  defstruct base_url: "",
            mount: "/q",
            signing: nil,
            encryption: nil,
            separator: ":",
            option_names: :short

  @doc """
  A builder from the options above; an unknown option raises `KeyError`.

      iex> XtandardImage.Query.new(mount: "/img/q").mount
      "/img/q"
  """
  @spec new(keyword()) :: t()
  def new(opts \\ []), do: struct!(__MODULE__, opts)

  @doc """
  Builds a native `/q/?url=…` URL. `opts`: `:params` (native keys), `:options`
  (imgproxy processing options, written as `o=`; a list of pipelines chains),
  `:extension` (`format=`), `:pdf_password` (`pdp=`; needs both keys).
  """
  @spec url(t(), String.t(), keyword()) :: {:ok, String.t()} | {:error, Error.t()}
  def url(builder, source, opts \\ []), do: Error.wrap(fn -> url!(builder, source, opts) end)

  @doc "Like `url/3`, raising `XtandardImage.Error`."
  @spec url!(t(), String.t(), keyword()) :: String.t()
  def url!(%__MODULE__{} = q, source, opts \\ []) do
    opts = Keyword.validate!(opts, [:params, :options, :extension, :pdf_password])

    build!(q, :process, Options.pipelines_of!(opts[:options]), source,
      params: opts[:params],
      extension: opts[:extension],
      pdf_password: opts[:pdf_password]
    )
  end

  @doc """
  Builds a `/q/video` URL: every video option is a key, its arguments joined
  by `,`; `crop` and `flip` are written `c` and `fl`. A list of option lists is
  written in order, so a key may repeat. `opts`: `:extension` (`format=`).

      iex> XtandardImage.query_urls()
      ...> |> XtandardImage.Query.video_url!("s3://m/talk.mov", [rs: {:fill, 640, 360}, vs: 1], extension: :webm)
      "/q/video?url=s3://m/talk.mov&rs=fill,640,360&vs=1&format=webm"
  """
  @spec video_url(
          t(),
          String.t(),
          XtandardImage.Video.options() | [XtandardImage.Video.options()] | nil,
          keyword()
        ) ::
          {:ok, String.t()} | {:error, Error.t()}
  def video_url(builder, source, options \\ nil, opts \\ []),
    do: Error.wrap(fn -> video_url!(builder, source, options, opts) end)

  @doc "Like `video_url/4`, raising `XtandardImage.Error`."
  @spec video_url!(
          t(),
          String.t(),
          XtandardImage.Video.options() | [XtandardImage.Video.options()] | nil,
          keyword()
        ) ::
          String.t()
  def video_url!(%__MODULE__{} = q, source, options \\ nil, opts \\ []) do
    opts = Keyword.validate!(opts, [:extension])
    entries = options |> Options.pipelines_of!() |> Enum.concat()
    build_video!(q, entries, source, opts[:extension])
  end

  @doc """
  Builds a `/q/video` HLS or DASH manifest URL (`format=m3u8|mpd`); `opts` as
  `XtandardImage.Imgproxy.stream_url/3` without `:source_encoding`.

      iex> XtandardImage.query_urls()
      ...> |> XtandardImage.Query.stream_url!("s3://m/talk.mov", protocol: :hls, renditions: [360, 720])
      "/q/video?url=s3://m/talk.mov&st=hls,360,720&format=m3u8"
  """
  @spec stream_url(t(), String.t(), keyword()) :: {:ok, String.t()} | {:error, Error.t()}
  def stream_url(builder, source, opts),
    do: Error.wrap(fn -> stream_url!(builder, source, opts) end)

  @doc "Like `stream_url/3`, raising `XtandardImage.Error`."
  @spec stream_url!(t(), String.t(), keyword()) :: String.t()
  def stream_url!(%__MODULE__{} = q, source, opts) do
    opts =
      Keyword.validate!(opts, [:protocol, :renditions, :segment_duration, :segment_format, :options])

    {entries, ext} = Imgproxy.stream_parts!(Options.pipelines_of!(opts[:options]), opts)
    build_video!(q, entries, source, ext)
  end

  @doc """
  Builds an imaginary endpoint URL (`/q/resize?url=…&width=300`). An endpoint
  that is not lower-case letters, or is `video`, is an `:invalid_endpoint`.

      iex> XtandardImage.query_urls()
      ...> |> XtandardImage.Query.imaginary_url!(:resize, "s3://b/cat.jpg", width: 300)
      "/q/resize?url=s3://b/cat.jpg&width=300"
  """
  @spec imaginary_url(t(), Spec.imaginary_endpoint(), String.t(), params() | nil) ::
          {:ok, String.t()} | {:error, Error.t()}
  def imaginary_url(builder, endpoint, source, params \\ nil),
    do: Error.wrap(fn -> imaginary_url!(builder, endpoint, source, params) end)

  @doc "Like `imaginary_url/4`, raising `XtandardImage.Error`."
  @spec imaginary_url!(t(), Spec.imaginary_endpoint(), String.t(), params() | nil) :: String.t()
  def imaginary_url!(%__MODULE__{} = q, endpoint, source, params \\ nil) do
    ep = if is_atom(endpoint) and endpoint != nil, do: Atom.to_string(endpoint), else: endpoint

    if not (is_binary(ep) and ep != "video" and lower?(ep)),
      do: Error.raise!(:invalid_endpoint, "invalid imaginary endpoint #{inspect(endpoint)}")

    url = Text.check_text!(source, :invalid_source, "source")
    finish(q, "/" <> ep, [{"url", url} | param_pairs!(params)])
  end

  @doc """
  Starts a fluent `/q/?url=…` URL: the `XtandardImage.Processing` functions
  write `o=`, `XtandardImage.Chain.param/3` a native key.

      iex> alias XtandardImage.Processing, as: P
      iex> XtandardImage.query_urls()
      ...> |> XtandardImage.Query.image("https://example.com/a.jpg")
      ...> |> P.param(:w, 300)
      ...> |> P.blur(2)
      ...> |> P.build!()
      "/q/?url=https://example.com/a.jpg&w=300&o=bl:2"
  """
  @spec image(t(), String.t()) :: Chain.t()
  def image(%__MODULE__{} = q, source), do: Chain.new(q, :process, source)

  @doc "Starts a fluent `/q/video` URL (see `XtandardImage.Video`)."
  @spec video(t(), String.t()) :: Chain.t()
  def video(%__MODULE__{} = q, source), do: Chain.new(q, :video, source)

  # ---- building ---------------------------------------------------------------

  @doc false
  @spec build!(t(), :process | :video, [Options.pipeline()], term(), keyword()) :: String.t()
  def build!(%__MODULE__{} = q, :process, pipelines, source, opts) do
    sep = Imgproxy.check_separator!(q.separator, Spec.forbidden_query_separators())
    style = Options.style!(q.option_names)
    url = Text.check_text!(source, :invalid_source, "source")
    pairs = [{"url", url} | param_pairs!(opts[:params])]

    o =
      case Enum.reject(pipelines, &(&1 == [])) do
        [] ->
          []

        non_empty ->
          path =
            Enum.map_join(non_empty, "/-/", fn p ->
              Enum.map_join(p, "/", &Options.format_entry!(:processing, &1, sep, style))
            end)

          [{"o", path}]
      end

    format = format_pair!(opts[:extension])

    pdp =
      case opts[:pdf_password] do
        nil ->
          []

        password ->
          [
            {Imgproxy.password_name(style),
             Imgproxy.password_token!(q.encryption, q.signing, password)}
          ]
      end

    finish(q, "/", pairs ++ o ++ format ++ pdp)
  end

  def build!(%__MODULE__{} = q, :video, pipelines, source, opts) do
    if opts[:pdf_password] != nil,
      do: Error.raise!(:invalid_request, "/q/video URLs take no pdf_password")

    if opts[:params] not in [nil, []],
      do: Error.raise!(:invalid_request, "/q/video URLs take video options, not native keys")

    if length(Enum.reject(pipelines, &(&1 == []))) > 1,
      do: Error.raise!(:invalid_request, "/q/video URLs have no chained pipelines")

    build_video!(q, Enum.concat(pipelines), source, opts[:extension])
  end

  defp build_video!(q, entries, source, ext) do
    style = Options.style!(q.option_names)
    url = Text.check_text!(source, :invalid_source, "source")

    pairs =
      Enum.map(entries, fn {name, args} ->
        key = Options.written_name!(:video, name, style)

        if args == [],
          do: Error.raise!(:invalid_argument, "option #{name}: at least one argument is required")

        values =
          args
          |> Enum.with_index(1)
          |> Enum.map_join(",", fn {a, i} -> video_arg!(a, "option #{name} argument #{i}") end)

        {Map.get(Spec.native_video_keys(), key, key), values}
      end)

    finish(q, "/video", [{"url", url} | pairs] ++ format_pair!(ext))
  end

  defp video_arg!(a, where) do
    s = Options.format_arg!(a, ",", where)

    if String.contains?(s, ":"),
      do: Error.raise!(:invalid_argument, "#{where}: #{inspect(s)} contains ':'")

    s
  end

  defp format_pair!(ext) when ext in [nil, ""], do: []
  defp format_pair!(ext), do: [{"format", Text.check_extension!(ext)}]

  defp param_pairs!(nil), do: []
  defp param_pairs!(params) when is_map(params), do: params |> Map.to_list() |> param_pairs!()

  defp param_pairs!(params) when is_list(params) do
    Enum.flat_map(params, fn
      {_k, nil} ->
        []

      {k, v} ->
        [{key!(k), native_value!(k, v)}]

      other ->
        Error.raise!(
          :invalid_argument,
          "a query param is a {key, value} pair, got #{inspect(other)}"
        )
    end)
  end

  defp param_pairs!(other),
    do:
      Error.raise!(
        :invalid_argument,
        "params must be a keyword list or a map, got #{inspect(other)}"
      )

  defp key!(k) when is_binary(k), do: k
  defp key!(k) when is_atom(k), do: Atom.to_string(k)
  defp key!(k), do: Error.raise!(:invalid_argument, "invalid query key #{inspect(k)}")

  defp native_value!(_k, true), do: "true"
  defp native_value!(_k, false), do: "false"
  defp native_value!(_k, v) when is_number(v), do: Text.format_number(v)
  defp native_value!(_k, v) when is_binary(v), do: v
  defp native_value!(_k, v) when is_atom(v), do: Atom.to_string(v)

  defp native_value!(k, v),
    do: Error.raise!(:invalid_argument, "query key #{key!(k)}: unsupported value #{inspect(v)}")

  defp finish(q, path, pairs) do
    base = String.trim_trailing(q.base_url, "/")
    mount = String.trim_trailing(q.mount, "/")

    query =
      Enum.map_join(pairs, "&", fn {k, v} ->
        Text.escape_query_value(k) <> "=" <> Text.escape_query_value(v)
      end)

    query = if q.signing, do: Signing.sign_xi_query!(path, query, q.signing), else: query
    base <> mount <> path <> "?" <> query
  end

  defp lower?(<<c, rest::binary>>) when c in ?a..?z, do: lower_rest?(rest)
  defp lower?(_), do: false
  defp lower_rest?(<<c, rest::binary>>) when c in ?a..?z, do: lower_rest?(rest)
  defp lower_rest?(<<>>), do: true
  defp lower_rest?(_), do: false
end
