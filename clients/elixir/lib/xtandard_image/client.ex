if Code.ensure_loaded?(Req) do
  defmodule XtandardImage.Readiness do
    @moduledoc "The `/readyz` answer: `ready` is true only for 200 `ready`."

    @type state :: :ready | :starting | :draining
    @type t :: %__MODULE__{ready: boolean(), state: state()}

    @enforce_keys [:ready, :state]
    defstruct [:ready, :state]
  end

  defmodule XtandardImage.StreamManifest do
    @moduledoc """
    A fetched HLS playlist or DASH MPD.

      * `url` - the (signed) manifest URL; hand it to a player.
      * `content_type` - `application/vnd.apple.mpegurl` or `application/dash+xml`.
      * `body` - the manifest. Its child URLs (`/_stream/<ticket>/…`) carry a
        ticket and need no signing: fetch them with `XtandardImage.Client.media/3`.
    """

    @type t :: %__MODULE__{url: String.t(), content_type: String.t(), body: String.t()}

    @enforce_keys [:url, :content_type, :body]
    defstruct [:url, :content_type, :body]
  end

  defmodule XtandardImage.Client do
    @moduledoc """
    An HTTP client for xtandard-image on [Req](https://hex.pm/packages/req),
    compiled when Req is a dependency of your project (`{:req, "~> 0.7"}`).

    It covers the JSON and health endpoints (`/info`, `/healthz`, `/readyz`, the
    playground's `api/meta`) and media fetches (images, `/video` results with
    Range and HEAD, HLS and DASH manifests and their parts). URLs are built and
    signed with `XtandardImage.Imgproxy`.

        xi = XtandardImage.Client.new("http://localhost:8080", signing: key)
        {:ok, info} = XtandardImage.Client.info(xi, "s3://photos/cat.jpg", size: true, format: true)
        info.width
        {:ok, resp} = XtandardImage.Client.video(xi, "s3://media/talk.mov", [vd: 5], extension: :webm, range: 0..1023)
        resp.status
        #=> 206

    Every function returns `{:ok, result}` or `{:error, error}` and has a bang
    variant that raises the error:

      * `XtandardImage.Error` - the URL could not be built;
      * `XtandardImage.HTTPError` - a non-2xx response, with `status`, `reason`
        (`:not_found`, `:forbidden`, …), `kind`, `message` and `request_id` from
        the server's JSON error envelope;
      * `XtandardImage.TransportError` - no response (connection, TLS, timeout).

    Media functions return the `Req.Response`, whose body is the bytes; pass
    `into:` (a function, a collectable or `:self`, as in `Req.request/2`) to
    stream a large body instead.

    ## Options (`new/2`)

      * `:signing`, `:encryption`, `:unsigned_signature`, `:source_encoding`,
        `:split_source`, `:separator`, `:option_names` - as in
        `XtandardImage.Imgproxy`;
      * `:headers` - headers sent with every request;
      * `:req_options` - options merged into the `Req.Request` (timeouts,
        `:retry`, `:plug` or `:adapter` in tests, …); the client sets
        `retry: false` and `decode_body: false` unless you override them;
      * `:playground_base_url` - origin of the admin listener for
        `playground_meta/1`; default the base URL;
      * `:playground_path` - `XI_PLAYGROUND_PATH`, default `"/_playground/"`.
    """

    alias XtandardImage.Error
    alias XtandardImage.HTTPError
    alias XtandardImage.Imgproxy
    alias XtandardImage.InfoResponse
    alias XtandardImage.Readiness
    alias XtandardImage.Spec
    alias XtandardImage.StreamManifest
    alias XtandardImage.TransportError

    @type t :: %__MODULE__{
            base_url: String.t(),
            urls: Imgproxy.t(),
            req: Req.Request.t(),
            playground_meta_url: String.t()
          }

    @typedoc "Any error a client function returns."
    @type error :: Error.t() | HTTPError.t() | TransportError.t()

    @typedoc "Options of the media functions."
    @type media_option ::
            {:method, :get | :head}
            | {:range, XtandardImage.Range.t()}
            | {:accept, String.t()}
            | {:headers, [{String.t(), String.t()}] | %{optional(String.t()) => String.t()}}
            | {:into, term()}

    @enforce_keys [:base_url, :urls, :req, :playground_meta_url]
    defstruct [:base_url, :urls, :req, :playground_meta_url]

    @builder_opts [
      :signing,
      :encryption,
      :unsigned_signature,
      :source_encoding,
      :split_source,
      :separator,
      :option_names
    ]
    @media_opts [:method, :range, :accept, :headers, :into]

    @doc """
    A client for a server origin (plus the imgproxy mount prefix, if any); see
    the options above. An unknown option raises `ArgumentError`.
    """
    @spec new(String.t(), keyword()) :: t()
    def new(base_url, opts \\ []) when is_binary(base_url) do
      opts =
        Keyword.validate!(
          opts,
          @builder_opts ++
            [
              headers: [],
              req_options: [],
              playground_base_url: nil,
              playground_path: "/_playground/"
            ]
        )

      base = String.trim_trailing(base_url, "/")
      urls = Imgproxy.new([base_url: base] ++ Keyword.take(opts, @builder_opts))
      origin = String.trim_trailing(opts[:playground_base_url] || base, "/")
      path = String.trim(opts[:playground_path], "/")

      req =
        [retry: false, decode_body: false, headers: opts[:headers]]
        |> Keyword.merge(opts[:req_options])
        |> Req.new()

      %__MODULE__{
        base_url: base,
        urls: urls,
        req: req,
        playground_meta_url: "#{origin}/#{path}/api/meta"
      }
    end

    @doc "`GET /healthz`: `:ok` while the process can serve HTTP."
    @spec healthz(t()) :: :ok | {:error, error()}
    def healthz(%__MODULE__{} = c) do
      with {:ok, _} <- checked(c, :get, c.base_url <> "/healthz", "text/plain") do
        :ok
      end
    end

    @doc "Like `healthz/1`, raising the error."
    @spec healthz!(t()) :: :ok
    def healthz!(c), do: c |> healthz() |> bang_ok()

    @doc "`GET /readyz`: the readiness state; 503 (`starting`, `draining`) is not an error."
    @spec readyz(t()) :: {:ok, Readiness.t()} | {:error, error()}
    def readyz(%__MODULE__{} = c) do
      url = c.base_url <> "/readyz"

      with {:ok, resp} <- send_request(c, :get, url, headers("text/plain", nil, nil), []) do
        state = resp.body |> body_text() |> String.trim()

        cond do
          resp.status not in [200, 503] ->
            {:error, HTTPError.from_response(resp.status, url, body_text(resp.body))}

          state in ["ready", "starting", "draining"] ->
            state = String.to_existing_atom(state)
            {:ok, %Readiness{ready: resp.status == 200 and state == :ready, state: state}}

          true ->
            {:error,
             %HTTPError{
               status: resp.status,
               reason: HTTPError.reason(resp.status),
               kind: "unknown",
               message: "unexpected readiness state #{inspect(state)}",
               url: url
             }}
        end
      end
    end

    @doc "Like `readyz/1`, raising the error."
    @spec readyz!(t()) :: Readiness.t()
    def readyz!(c), do: c |> readyz() |> bang()

    @doc """
    `GET /info` for a source, with the URL built (and signed) from the
    arguments; `opts`: `:source_encoding`, `:pdf_password`.
    """
    @spec info(t(), String.t(), XtandardImage.Info.options() | nil, keyword()) ::
            {:ok, InfoResponse.t()} | {:error, error()}
    def info(%__MODULE__{} = c, source, options \\ nil, opts \\ []) do
      with {:ok, url} <- Imgproxy.info_url(c.urls, source, options, opts) do
        info_at(c, url)
      end
    end

    @doc "Like `info/4`, raising the error."
    @spec info!(t(), String.t(), XtandardImage.Info.options() | nil, keyword()) :: InfoResponse.t()
    def info!(c, source, options \\ nil, opts \\ []), do: c |> info(source, options, opts) |> bang()

    @doc "`GET` a prebuilt `/info/…` path or URL."
    @spec info_at(t(), String.t()) :: {:ok, InfoResponse.t()} | {:error, error()}
    def info_at(%__MODULE__{} = c, url) do
      with {:ok, map} <- json(c, absolute(c, url)) do
        {:ok, InfoResponse.from_map(map)}
      end
    end

    @doc "Like `info_at/2`, raising the error."
    @spec info_at!(t(), String.t()) :: InfoResponse.t()
    def info_at!(c, url), do: c |> info_at(url) |> bang()

    @doc """
    `GET` (or `HEAD`) any URL or server path: an image or `/video` URL, a query
    URL, a stream part. `opts`: `:method` (`:get` or `:head`), `:range`
    (`t:XtandardImage.Range.t/0`), `:accept` (default `*/*`), `:headers`, `:into`.
    """
    @spec media(t(), String.t(), [media_option()]) :: {:ok, Req.Response.t()} | {:error, error()}
    def media(%__MODULE__{} = c, url, opts \\ []) do
      opts = Keyword.validate!(opts, @media_opts)

      with {:ok, range} <- range(opts[:range]) do
        url = absolute(c, url)
        h = headers(opts[:accept] || "*/*", range, opts[:headers])
        extra = if opts[:into] == nil, do: [], else: [into: opts[:into]]
        c |> send_request(opts[:method] || :get, url, h, extra) |> check(url)
      end
    end

    @doc "Like `media/3`, raising the error."
    @spec media!(t(), String.t(), [media_option()]) :: Req.Response.t()
    def media!(c, url, opts \\ []), do: c |> media(url, opts) |> bang()

    @doc """
    `GET` (or `HEAD`) a processed image. `opts`: `:extension`,
    `:source_encoding`, `:pdf_password`, and the `media/3` options (`:accept`
    drives format negotiation).
    """
    @spec image(t(), String.t(), Imgproxy.options(), keyword()) ::
            {:ok, Req.Response.t()} | {:error, error()}
    def image(%__MODULE__{} = c, source, options \\ nil, opts \\ []) do
      {media_opts, url_opts} = Keyword.split(opts, @media_opts)

      with {:ok, url} <- Imgproxy.url(c.urls, source, options, url_opts) do
        media(c, url, media_opts)
      end
    end

    @doc "Like `image/4`, raising the error."
    @spec image!(t(), String.t(), Imgproxy.options(), keyword()) :: Req.Response.t()
    def image!(c, source, options \\ nil, opts \\ []),
      do: c |> image(source, options, opts) |> bang()

    @doc """
    `GET` (or `HEAD`) a `/video` result: video, audio or a waveform. `opts`:
    `:extension` (the container), `:source_encoding`, and the `media/3`
    options; `:range` asks for part of it (206).
    """
    @spec video(t(), String.t(), XtandardImage.Video.options() | nil, keyword()) ::
            {:ok, Req.Response.t()} | {:error, error()}
    def video(%__MODULE__{} = c, source, options \\ nil, opts \\ []) do
      {media_opts, url_opts} = Keyword.split(opts, @media_opts)

      with {:ok, url} <- Imgproxy.video_url(c.urls, source, options, url_opts) do
        media(c, url, media_opts)
      end
    end

    @doc "Like `video/4`, raising the error."
    @spec video!(t(), String.t(), XtandardImage.Video.options() | nil, keyword()) ::
            Req.Response.t()
    def video!(c, source, options \\ nil, opts \\ []),
      do: c |> video(source, options, opts) |> bang()

    @doc """
    `GET` an HLS or DASH manifest (`/video/…@m3u8` or `@mpd`). `opts` as
    `XtandardImage.Imgproxy.stream_url/3`, plus `:headers`.
    """
    @spec stream(t(), String.t(), keyword()) :: {:ok, StreamManifest.t()} | {:error, error()}
    def stream(%__MODULE__{} = c, source, opts) do
      {headers, url_opts} = Keyword.pop(opts, :headers)

      with {:ok, url} <- Imgproxy.stream_url(c.urls, source, url_opts) do
        ctype = manifest_type(opts[:protocol])

        with {:ok, resp} <-
               c
               |> send_request(:get, url, headers("#{ctype}, */*;q=0.5", nil, headers), [])
               |> check(url) do
          content_type = resp |> Req.Response.get_header("content-type") |> List.first(ctype)
          {:ok, %StreamManifest{url: url, content_type: content_type, body: body_text(resp.body)}}
        end
      end
    end

    @doc "Like `stream/3`, raising the error."
    @spec stream!(t(), String.t(), keyword()) :: StreamManifest.t()
    def stream!(c, source, opts), do: c |> stream(source, opts) |> bang()

    @doc """
    `GET` the playground's `api/meta` (needs `XI_PLAYGROUND_ENABLED`; served on
    the admin listener by default).
    """
    @spec playground_meta(t()) :: {:ok, map()} | {:error, error()}
    def playground_meta(%__MODULE__{} = c), do: json(c, c.playground_meta_url)

    @doc "Like `playground_meta/1`, raising the error."
    @spec playground_meta!(t()) :: map()
    def playground_meta!(c), do: c |> playground_meta() |> bang()

    # ---- internals -------------------------------------------------------------

    defp json(c, url) do
      with {:ok, resp} <- checked(c, :get, url, "application/json") do
        case XtandardImage.JSON.decode(body_text(resp.body)) do
          {:ok, map} when is_map(map) ->
            {:ok, map}

          _ ->
            {:error,
             %HTTPError{
               status: resp.status,
               reason: :http_error,
               kind: "unknown",
               message: "the response is not a JSON object",
               url: url
             }}
        end
      end
    end

    defp checked(c, method, url, accept),
      do: c |> send_request(method, url, headers(accept, nil, nil), []) |> check(url)

    defp send_request(c, method, url, headers, extra) do
      case Req.request(c.req, [method: method, url: url, headers: headers] ++ extra) do
        {:ok, %Req.Response{} = resp} ->
          {:ok, resp}

        {:error, exception} ->
          {:error,
           %TransportError{
             reason: transport_reason(exception),
             url: url,
             message:
               "#{method |> Atom.to_string() |> String.upcase()} #{url}: #{Exception.message(exception)}"
           }}
      end
    end

    defp transport_reason(%Req.TransportError{reason: reason}), do: reason
    defp transport_reason(exception), do: exception

    defp check({:ok, %Req.Response{status: status} = resp}, _url) when status in 200..299,
      do: {:ok, resp}

    defp check({:ok, %Req.Response{} = resp}, url),
      do: {:error, HTTPError.from_response(resp.status, url, body_text(resp.body))}

    defp check({:error, _} = error, _url), do: error

    defp headers(accept, range, extra) do
      base = Enum.map(extra || [], fn {k, v} -> {String.downcase(to_string(k)), v} end)
      base = [{"accept", accept} | Enum.reject(base, fn {k, _} -> k == "accept" end)]
      if range, do: [{"range", range} | base], else: base
    end

    defp range(nil), do: {:ok, nil}
    defp range(r), do: XtandardImage.Range.header(r)

    defp absolute(c, url) do
      cond do
        Regex.match?(~r/^[a-zA-Z][a-zA-Z0-9+.-]*:/, url) -> url
        String.starts_with?(url, "/") -> c.base_url <> url
        true -> c.base_url <> "/" <> url
      end
    end

    defp manifest_type(protocol) do
      p = if is_atom(protocol) and protocol != nil, do: Atom.to_string(protocol), else: protocol

      case Map.get(Spec.stream_manifests(), p) do
        {_container, type} -> type
        nil -> "application/octet-stream"
      end
    end

    defp body_text(body) when is_binary(body), do: body
    defp body_text(_body), do: ""

    defp bang_ok(:ok), do: :ok
    defp bang_ok({:error, error}), do: raise(error)

    defp bang({:ok, value}), do: value
    defp bang({:error, error}), do: raise(error)
  end
end
