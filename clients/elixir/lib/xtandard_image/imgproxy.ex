defmodule XtandardImage.Imgproxy do
  @moduledoc """
  imgproxy-dialect URL builder:

      {base_url}[/info]/{signature}/{option}/…[/-/{option}/…]/{source}
      {base_url}/video/{signature}/{video option}/…/{source}   (signs "/video" <> path)

  Sources are base64url (default), `plain/` (percent-escaped, `@ext`) or `enc/`
  (AES-CBC); base64 and `enc/` sources take `.ext`.

  Options are a keyword list (or a map) of option names or aliases to their
  arguments, in URL order: a tuple or list, or the first argument alone when
  the others are optional. A list of keyword lists chains pipelines (`/-/`).

      iex> img = XtandardImage.imgproxy()
      iex> XtandardImage.Imgproxy.url(img, "https://example.com/a.jpg", [rs: {:fill, 300, 400}, q: 80], extension: "webp")
      {:ok, "/insecure/rs:fill:300:400/q:80/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw.webp"}
      iex> XtandardImage.Imgproxy.url(img, "https://example.com/a.jpg", nope: 1)
      {:error, %XtandardImage.Error{code: :unknown_option, message: ~s(unknown imgproxy option "nope")}}

  ## Configuration (`new/1`)

    * `:base_url` - prepended to every path: the server origin and the
      dialect's mount prefix, if any. Default `""` (root-relative paths).
    * `:signing` - an `XtandardImage.SigningKey`; without it the signature
      segment is `:unsigned_signature`.
    * `:unsigned_signature` - `"insecure"` (default), `"unsafe"` or `"_"`.
    * `:encryption` - an `XtandardImage.EncryptionKey` for `:encrypted`
      sources and PDF passwords.
    * `:source_encoding` - `:base64` (default), `:plain` or `:encrypted`.
    * `:split_source` - splits base64 and encrypted sources into
      `/`-separated chunks of this many characters.
    * `:separator` - the argument separator
      (`XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR`), default `":"`.
    * `:option_names` - `:short` (default), `:canonical` or `:given` (as
      written).
  """

  alias XtandardImage.Chain
  alias XtandardImage.Encryption
  alias XtandardImage.EncryptionKey
  alias XtandardImage.Error
  alias XtandardImage.Options
  alias XtandardImage.Signing
  alias XtandardImage.SigningKey
  alias XtandardImage.Spec
  alias XtandardImage.Text

  @type t :: %__MODULE__{
          base_url: String.t(),
          signing: SigningKey.t() | nil,
          unsigned_signature: String.t(),
          encryption: EncryptionKey.t() | nil,
          source_encoding: Spec.source_encoding(),
          split_source: pos_integer() | nil,
          separator: String.t(),
          option_names: :short | :canonical | :given
        }

  @typedoc "Processing options: one pipeline, or a list of pipelines to chain."
  @type options :: XtandardImage.Processing.options() | [XtandardImage.Processing.options()] | nil

  @typedoc "The URL kinds of this dialect."
  @type endpoint :: :process | :info | :video

  defstruct base_url: "",
            signing: nil,
            unsigned_signature: "insecure",
            encryption: nil,
            source_encoding: :base64,
            split_source: nil,
            separator: ":",
            option_names: :short

  @stream_options ~w(stream segment_duration segment_format format)

  @doc """
  A builder from the options above; an unknown option raises `KeyError`.

      iex> XtandardImage.Imgproxy.new(base_url: "https://img.example.com").base_url
      "https://img.example.com"
  """
  @spec new(keyword()) :: t()
  def new(opts \\ []), do: struct!(__MODULE__, opts)

  @doc """
  Builds a processing URL. `opts`: `:extension` (output format),
  `:source_encoding` (overrides the builder's), `:pdf_password` (plain-text
  password of a protected PDF; needs `:encryption` and `:signing`).
  """
  @spec url(t(), String.t(), options(), keyword()) :: {:ok, String.t()} | {:error, Error.t()}
  def url(builder, source, options \\ nil, opts \\ []),
    do: Error.wrap(fn -> url!(builder, source, options, opts) end)

  @doc "Like `url/4`, raising `XtandardImage.Error`."
  @spec url!(t(), String.t(), options(), keyword()) :: String.t()
  def url!(%__MODULE__{} = b, source, options \\ nil, opts \\ []) do
    opts = Keyword.validate!(opts, [:extension, :source_encoding, :pdf_password])

    build!(b, :process, Options.pipelines_of!(options), source,
      encoding: opts[:source_encoding],
      extension: opts[:extension],
      pdf_password: opts[:pdf_password]
    )
  end

  @doc """
  Builds an `/info` URL (one pipeline, no extension). `opts`:
  `:source_encoding`, `:pdf_password`.

      iex> XtandardImage.imgproxy(source_encoding: :plain)
      ...> |> XtandardImage.Imgproxy.info_url!("s3://b/cat.jpg", size: true, format: true)
      "/info/insecure/s:1/f:1/plain/s3://b/cat.jpg"
  """
  @spec info_url(t(), String.t(), XtandardImage.Info.options() | nil, keyword()) ::
          {:ok, String.t()} | {:error, Error.t()}
  def info_url(builder, source, options \\ nil, opts \\ []),
    do: Error.wrap(fn -> info_url!(builder, source, options, opts) end)

  @doc "Like `info_url/4`, raising `XtandardImage.Error`."
  @spec info_url!(t(), String.t(), XtandardImage.Info.options() | nil, keyword()) :: String.t()
  def info_url!(%__MODULE__{} = b, source, options \\ nil, opts \\ []) do
    opts = Keyword.validate!(opts, [:source_encoding, :pdf_password])

    build!(b, :info, Options.pipelines_of!(options), source,
      encoding: opts[:source_encoding],
      pdf_password: opts[:pdf_password]
    )
  end

  @doc """
  Builds a `/video` URL: a video transformation, audio or a waveform. `opts`:
  `:extension` (the container: `webm`, `mp4`, `mp3`, `png`, …),
  `:source_encoding`.

      iex> XtandardImage.imgproxy(source_encoding: :plain)
      ...> |> XtandardImage.Imgproxy.video_url!("s3://m/a.mov", [rs: {:fill, 640, 360}, vs: 1.5, vd: 5], extension: :webm)
      "/video/insecure/rs:fill:640:360/vs:1.5/vd:5/plain/s3://m/a.mov@webm"
  """
  @spec video_url(t(), String.t(), XtandardImage.Video.options() | nil, keyword()) ::
          {:ok, String.t()} | {:error, Error.t()}
  def video_url(builder, source, options \\ nil, opts \\ []),
    do: Error.wrap(fn -> video_url!(builder, source, options, opts) end)

  @doc "Like `video_url/4`, raising `XtandardImage.Error`."
  @spec video_url!(t(), String.t(), XtandardImage.Video.options() | nil, keyword()) :: String.t()
  def video_url!(%__MODULE__{} = b, source, options \\ nil, opts \\ []) do
    opts = Keyword.validate!(opts, [:extension, :source_encoding])

    build!(b, :video, Options.pipelines_of!(options), source,
      encoding: opts[:source_encoding],
      extension: opts[:extension]
    )
  end

  @doc """
  Builds an HLS or DASH manifest URL (`/video/…@m3u8` or `@mpd`). `opts`:

    * `:protocol` (required) - `:hls` or `:dash`;
    * `:renditions` - rungs by the short side of the frame (at most 8);
      default: the server's ladder;
    * `:segment_duration` - segment length in whole seconds (1 to 30);
    * `:segment_format` - `:fmp4` (CMAF, default) or `:ts` (MPEG-TS, HLS only);
    * `:options` - other `/video` options (trim, crop, quality, audio, …)
      applied to every rendition;
    * `:source_encoding`.

  `stream`, `segment_duration`, `segment_format` and `format` are refused in
  `:options` (`:invalid_request`).

      iex> XtandardImage.imgproxy(source_encoding: :plain)
      ...> |> XtandardImage.Imgproxy.stream_url!("s3://m/a.mov", protocol: :hls, renditions: [360, 720])
      "/video/insecure/st:hls:360:720/plain/s3://m/a.mov@m3u8"
  """
  @spec stream_url(t(), String.t(), keyword()) :: {:ok, String.t()} | {:error, Error.t()}
  def stream_url(builder, source, opts),
    do: Error.wrap(fn -> stream_url!(builder, source, opts) end)

  @doc "Like `stream_url/3`, raising `XtandardImage.Error`."
  @spec stream_url!(t(), String.t(), keyword()) :: String.t()
  def stream_url!(%__MODULE__{} = b, source, opts) do
    opts =
      Keyword.validate!(opts, [
        :protocol,
        :renditions,
        :segment_duration,
        :segment_format,
        :options,
        :source_encoding
      ])

    {entries, ext} = stream_parts!(Options.pipelines_of!(opts[:options]), opts)
    build!(b, :video, [entries], source, encoding: opts[:source_encoding], extension: ext)
  end

  @doc """
  Starts a fluent processing URL (see `XtandardImage.Processing`).

      iex> alias XtandardImage.Processing, as: P
      iex> XtandardImage.imgproxy(option_names: :given, source_encoding: :plain)
      ...> |> XtandardImage.Imgproxy.image("https://example.com/a.jpg")
      ...> |> P.resize([:fit, 1, 2])
      ...> |> P.q(90)
      ...> |> P.min_width(10)
      ...> |> to_string()
      "/insecure/resize:fit:1:2/q:90/min-width:10/plain/https://example.com/a.jpg"
  """
  @spec image(t(), String.t()) :: Chain.t()
  def image(%__MODULE__{} = b, source), do: Chain.new(b, :process, source)

  @doc "Starts a fluent `/info` URL (see `XtandardImage.Info`)."
  @spec info(t(), String.t()) :: Chain.t()
  def info(%__MODULE__{} = b, source), do: Chain.new(b, :info, source)

  @doc "Starts a fluent `/video` URL (see `XtandardImage.Video`)."
  @spec video(t(), String.t()) :: Chain.t()
  def video(%__MODULE__{} = b, source), do: Chain.new(b, :video, source)

  # ---- building ---------------------------------------------------------------

  @doc false
  # Builds a URL from normalised pipelines, checking in the order of
  # clients/README.md ("Checks run in this order").
  @spec build!(t(), endpoint(), [Options.pipeline()], term(), keyword()) :: String.t()
  def build!(%__MODULE__{} = cfg, endpoint, pipelines, source, opts) do
    sep = check_separator!(cfg.separator, Spec.forbidden_separators())
    style = Options.style!(cfg.option_names)
    pdf_password = opts[:pdf_password]
    non_empty = Enum.reject(pipelines, &(&1 == []))

    if endpoint != :process and length(non_empty) > 1,
      do: Error.raise!(:invalid_request, "/#{endpoint} URLs have no chained pipelines")

    if endpoint == :video and pdf_password != nil,
      do: Error.raise!(:invalid_request, "/video URLs take no pdf_password")

    password =
      if pdf_password == nil,
        do: [],
        else: [
          password_name(style) <> sep <> password_token!(cfg.encryption, cfg.signing, pdf_password)
        ]

    cat = catalogue(endpoint)

    options =
      non_empty
      |> Enum.map(fn p -> Enum.map(p, &Options.format_entry!(cat, &1, sep, style)) end)
      |> Enum.intersperse([Spec.pipeline_separator()])
      |> Enum.concat()

    encoding = opts[:encoding] || cfg.source_encoding
    src = source_segment!(cfg, source, encoding, opts[:extension])
    finish(cfg, endpoint, "/" <> Enum.join(password ++ options ++ [src], "/"))
  end

  @doc false
  @spec check_separator!(term(), [String.t()]) :: String.t()
  def check_separator!(sep, forbidden) do
    if is_binary(sep) and String.length(sep) == 1 and sep not in forbidden,
      do: sep,
      else: Error.raise!(:invalid_separator, "invalid argument separator #{inspect(sep)}")
  end

  @doc false
  @spec password_token!(EncryptionKey.t() | nil, SigningKey.t() | nil, term()) :: String.t()
  def password_token!(nil, _signing, _password),
    do: Error.raise!(:missing_encryption, "pdf_password needs an encryption key")

  def password_token!(_encryption, nil, _password),
    do:
      Error.raise!(
        :missing_signing,
        "pdf_password needs signing: the server refuses it on unsigned URLs"
      )

  def password_token!(%EncryptionKey{} = key, %SigningKey{}, password),
    do: Encryption.encrypt_pdf_password!(password, key)

  @doc false
  @spec password_name(Options.style()) :: String.t()
  def password_name(:canonical), do: Spec.pdf_password_name()
  def password_name(_style), do: Spec.pdf_password_short()

  @doc false
  # The video options and the manifest container of a stream request.
  @spec stream_parts!([Options.pipeline()], keyword()) :: {Options.pipeline(), String.t()}
  def stream_parts!(pipelines, opts) do
    entries = Enum.concat(pipelines)

    for {name, _} <- entries, Options.canonical(:video, name) in @stream_options do
      Error.raise!(
        :invalid_request,
        "stream requests set #{name} through protocol, renditions and the segment settings"
      )
    end

    protocol = to_text(opts[:protocol])

    {container, _type} =
      Map.get(Spec.stream_manifests(), protocol) ||
        Error.raise!(:invalid_request, "unknown stream protocol #{inspect(opts[:protocol])}")

    renditions =
      case opts[:renditions] do
        r when r in [nil, []] -> []
        r when is_list(r) -> [{"stream", [protocol | r]}]
        r -> Error.raise!(:invalid_argument, "renditions must be a list, got #{inspect(r)}")
      end

    duration =
      case opts[:segment_duration] do
        nil -> []
        d -> [{"segment_duration", [d]}]
      end

    format = segment_format!(opts[:segment_format], protocol)
    {entries ++ renditions ++ duration ++ format, container}
  end

  defp segment_format!(nil, _protocol), do: []

  defp segment_format!(format, protocol) do
    f = to_text(format)

    case Map.get(Spec.segment_format_protocols(), f) do
      protocols when is_list(protocols) ->
        if protocol not in protocols,
          do: Error.raise!(:invalid_request, "segment format #{f} is not available for #{protocol}")

      nil ->
        Error.raise!(:invalid_request, "unknown segment format #{inspect(format)}")
    end

    if f == Spec.default_segment_format(), do: [], else: [{"segment_format", [f]}]
  end

  defp to_text(a) when is_atom(a) and not is_nil(a), do: Atom.to_string(a)
  defp to_text(s), do: s

  defp catalogue(:process), do: :processing
  defp catalogue(:info), do: :info
  defp catalogue(:video), do: :video

  defp source_segment!(cfg, source, encoding, ext) do
    source = Text.check_text!(source, :invalid_source, "source")
    e = if ext in [nil, ""], do: nil, else: Text.check_extension!(ext)

    case to_text(encoding) do
      "plain" ->
        "plain/" <> Text.escape_plain_source(source) <> suffix("@", e)

      "base64" ->
        split(Text.b64url(source), cfg.split_source) <> suffix(".", e)

      "encrypted" ->
        key =
          cfg.encryption ||
            Error.raise!(:missing_encryption, "encrypted sources need an encryption key")

        "enc/" <> split(Encryption.encrypt_source!(source, key), cfg.split_source) <> suffix(".", e)

      _ ->
        Error.raise!(:invalid_request, "unknown source encoding #{inspect(encoding)}")
    end
  end

  defp suffix(_mark, nil), do: ""
  defp suffix(mark, ext), do: mark <> ext

  defp split(token, n) when is_integer(n) and n > 0 and byte_size(token) > n do
    token
    |> String.codepoints()
    |> Enum.chunk_every(n)
    |> Enum.map_join("/", &Enum.join/1)
  end

  defp split(token, _n), do: token

  defp finish(cfg, :video, path) do
    sig =
      if cfg.signing, do: Signing.sign("/video" <> path, cfg.signing), else: cfg.unsigned_signature

    base(cfg) <> "/video/" <> sig <> path
  end

  defp finish(cfg, endpoint, path) do
    signed =
      if cfg.signing,
        do: Signing.sign_imgproxy_path(path, cfg.signing),
        else: "/" <> cfg.unsigned_signature <> path

    base(cfg) <> if(endpoint == :info, do: "/info", else: "") <> signed
  end

  defp base(cfg), do: String.trim_trailing(cfg.base_url, "/")
end
