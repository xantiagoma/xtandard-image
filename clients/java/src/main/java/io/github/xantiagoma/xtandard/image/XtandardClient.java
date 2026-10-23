package io.github.xantiagoma.xtandard.image;

import java.io.IOException;
import java.net.http.HttpClient;
import java.net.http.HttpResponse;
import java.net.http.HttpResponse.BodyHandler;
import java.net.http.HttpResponse.BodyHandlers;
import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.Map;
import org.jspecify.annotations.Nullable;

/**
 * A synchronous HTTP client for xtandard-image on {@link HttpClient}: the JSON and health endpoints
 * ({@code /info}, {@code /healthz}, {@code /readyz}, the playground's {@code api/meta}) and media
 * fetches (images, {@code /video} results with Range and HEAD, HLS and DASH manifests). {@link
 * #async()} gives the same calls returning {@link java.util.concurrent.CompletableFuture}.
 *
 * <pre>{@code
 * XtandardClient xi = XtandardClient.builder("http://localhost:8080")
 *     .signing(SigningKey.of(keyHex, saltHex))
 *     .build();
 * InfoResponse info = xi.info("s3://bucket/cat.jpg", InfoOptions.create().size(true).format(true));
 * MediaResponse<byte[]> head = xi.video("s3://bucket/talk.mov", VideoOptions.create().duration(5),
 *     UrlParams.ofExtension("webm"), MediaRequest.head());
 * }</pre>
 *
 * <p>Non-2xx responses throw an {@link HttpException} subclass chosen by status; connection
 * failures throw {@link TransportException}. Thread-safe.
 */
public final class XtandardClient {
  private final ClientCore core;
  private final XtandardAsyncClient async;

  private XtandardClient(ClientCore core) {
    this.core = core;
    this.async = new XtandardAsyncClient(core);
  }

  /**
   * Starts a client builder.
   *
   * @param baseUrl the server origin, plus the imgproxy mount prefix if any
   * @return a new builder
   */
  public static Builder builder(String baseUrl) {
    return new Builder(baseUrl);
  }

  /** Settings of an {@link XtandardClient}. Not thread-safe. */
  public static final class Builder {
    private final ImgproxyUrlBuilder.Builder urls;
    private final String baseUrl;
    private final Map<String, String> headers = new LinkedHashMap<>();
    private Duration timeout = Duration.ofSeconds(60);
    private @Nullable HttpClient httpClient;
    private @Nullable String playgroundBaseUrl;
    private String playgroundPath = "/_playground/";
    private boolean queryMethod;
    private String queryMount = Constants.QUERY_MOUNT;

    private Builder(String baseUrl) {
      this.baseUrl = baseUrl;
      this.urls = ImgproxyUrlBuilder.builder().baseUrl(baseUrl);
    }

    /**
     * Signs the URLs the client builds.
     *
     * @param signing the key, or null for unsigned URLs
     * @return this
     */
    public Builder signing(@Nullable SigningKey signing) {
      urls.signing(signing);
      return this;
    }

    /**
     * The key for encrypted sources and PDF passwords.
     *
     * @param encryption the key, or null
     * @return this
     */
    public Builder encryption(@Nullable EncryptionKey encryption) {
      urls.encryption(encryption);
      return this;
    }

    /**
     * The signature segment of unsigned URLs.
     *
     * @param placeholder {@code insecure} (default), {@code unsafe} or {@code _}
     * @return this
     */
    public Builder unsignedSignature(String placeholder) {
      urls.unsignedSignature(placeholder);
      return this;
    }

    /**
     * The default source encoding.
     *
     * @param encoding the encoding
     * @return this
     */
    public Builder sourceEncoding(SourceEncoding encoding) {
      urls.sourceEncoding(encoding);
      return this;
    }

    /**
     * Splits base64 and encrypted sources into chunks.
     *
     * @param chunk the chunk length; 0 does not split
     * @return this
     */
    public Builder splitSource(int chunk) {
      urls.splitSource(chunk);
      return this;
    }

    /**
     * The argument separator.
     *
     * @param separator one character
     * @return this
     */
    public Builder separator(String separator) {
      urls.separator(separator);
      return this;
    }

    /**
     * How option names are written.
     *
     * @param style the style
     * @return this
     */
    public Builder optionNames(OptionNameStyle style) {
      urls.optionNames(style);
      return this;
    }

    /**
     * A header sent with every request.
     *
     * @param name the header name
     * @param value the value
     * @return this
     */
    public Builder header(String name, String value) {
      headers.put(name, value);
      return this;
    }

    /**
     * The request timeout (and, for a client this builder creates, the connect timeout). Default 60
     * seconds.
     *
     * @param timeout the timeout
     * @return this
     */
    public Builder timeout(Duration timeout) {
      this.timeout = timeout;
      return this;
    }

    /**
     * The {@link HttpClient} to send requests with; default one created for this client.
     *
     * @param httpClient the HTTP client
     * @return this
     */
    public Builder httpClient(HttpClient httpClient) {
      this.httpClient = httpClient;
      return this;
    }

    /**
     * The origin of the admin listener for {@link #playgroundMeta()}; default the base URL.
     *
     * @param origin the origin
     * @return this
     */
    public Builder playgroundBaseUrl(String origin) {
      this.playgroundBaseUrl = origin;
      return this;
    }

    /**
     * The playground path (XI_PLAYGROUND_PATH). Default {@code /_playground/}.
     *
     * @param path the path
     * @return this
     */
    public Builder playgroundPath(String path) {
      this.playgroundPath = path;
      return this;
    }

    /**
     * Sends query dialect URLs (those under {@link #queryMount(String)}) passed to {@code media()}
     * with the HTTP QUERY method (RFC 10008) when the call names no method ({@link
     * MediaRequest#of()}): the query moves into the request content. Off by default: the server
     * needs {@code XI_SERVER_QUERY_METHOD} (on by default), and many CDNs and proxies neither cache
     * nor forward QUERY. {@link MediaRequest#query()} does the same for one call.
     *
     * @param on whether to send query dialect URLs as QUERY
     * @return this
     */
    public Builder queryMethod(boolean on) {
      this.queryMethod = on;
      return this;
    }

    /**
     * The query dialect's mount (XI_DIALECTS_QUERY_PREFIX), for {@link #queryMethod(boolean)}.
     * Default {@code /q}.
     *
     * @param mount the mount
     * @return this
     */
    public Builder queryMount(String mount) {
      this.queryMount = mount;
      return this;
    }

    /**
     * Builds the client.
     *
     * @return the client
     */
    public XtandardClient build() {
      HttpClient h = httpClient;
      if (h == null) {
        h =
            HttpClient.newBuilder()
                .connectTimeout(timeout)
                .followRedirects(HttpClient.Redirect.NORMAL)
                .build();
      }
      ImgproxyUrlBuilder u = urls.build();
      String origin = playgroundBaseUrl == null ? baseUrl : playgroundBaseUrl;
      return new XtandardClient(
          new ClientCore(
              h,
              u,
              headers,
              timeout,
              ClientCore.playgroundMeta(origin, playgroundPath),
              queryMethod,
              queryMount));
    }
  }

  /**
   * The URL builder this client uses (same base URL and keys).
   *
   * @return the URL builder
   */
  public ImgproxyUrlBuilder urls() {
    return core.urls;
  }

  /**
   * The same client with {@link java.util.concurrent.CompletableFuture} results; it shares the
   * {@link HttpClient}.
   *
   * @return the async client
   */
  public XtandardAsyncClient async() {
    return async;
  }

  /**
   * Formats a byte range as a {@code Range} header value ({@code bytes=0-1023}, {@code
   * bytes=-500}).
   *
   * @param range the range
   * @return the header value
   */
  public static String rangeHeader(ByteRange range) {
    return range.header();
  }

  private <T> MediaResponse<T> send(String url, MediaRequest m, BodyHandler<T> handler) {
    try {
      HttpResponse<ClientCore.Checked<T>> res =
          core.http.send(core.request(url, m), ClientCore.checked(handler));
      return ClientCore.unwrap(res);
    } catch (IOException e) {
      throw new TransportException(core.method(url, m) + " " + url + ": " + e, e);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new TransportException(core.method(url, m) + " " + url + ": interrupted", e);
    }
  }

  private HttpResponse<byte[]> sendRaw(String url, MediaRequest m) {
    try {
      return core.http.send(core.request(url, m), BodyHandlers.ofByteArray());
    } catch (IOException e) {
      throw new TransportException(core.method(url, m) + " " + url + ": " + e, e);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new TransportException(core.method(url, m) + " " + url + ": interrupted", e);
    }
  }

  /**
   * {@code GET /healthz}: returns while the process can serve HTTP.
   *
   * @throws HttpException for a non-2xx answer
   */
  public void healthz() {
    send(
        core.baseUrl + "/healthz",
        MediaRequest.get().withAccept("text/plain"),
        BodyHandlers.discarding());
  }

  /**
   * {@code GET /readyz}: the readiness state; 503 (starting, draining) is not an error.
   *
   * @return the state
   */
  public Readiness readyz() {
    String url = core.baseUrl + "/readyz";
    HttpResponse<byte[]> res = sendRaw(url, MediaRequest.get().withAccept("text/plain"));
    return ClientCore.readiness(res.statusCode(), url, ClientCore.text(res.body()));
  }

  /**
   * {@code GET /info} for a source without options.
   *
   * @param source the source URL
   * @return the decoded answer
   */
  public InfoResponse info(String source) {
    return info(source, InfoOptions.create(), UrlParams.of());
  }

  /**
   * {@code GET /info} for a source, with the URL built (and signed) from the arguments.
   *
   * @param source the source URL
   * @param options the {@code /info} options
   * @return the decoded answer
   */
  public InfoResponse info(String source, InfoOptions options) {
    return info(source, options, UrlParams.of());
  }

  /**
   * {@code GET /info} with per-URL settings (source encoding, PDF password).
   *
   * @param source the source URL
   * @param options the {@code /info} options
   * @param params the source encoding and PDF password
   * @return the decoded answer
   */
  public InfoResponse info(String source, InfoOptions options, UrlParams params) {
    return infoAt(core.urls.infoUrl(source, options, params));
  }

  /**
   * {@code GET} a prebuilt {@code /info/...} path or URL.
   *
   * @param url the path or URL
   * @return the decoded answer
   */
  public InfoResponse infoAt(String url) {
    return ClientCore.info(
        send(
            core.abs(url),
            MediaRequest.get().withAccept("application/json"),
            BodyHandlers.ofByteArray()));
  }

  /**
   * {@code GET} any URL or server path: an image or video URL, a query URL, a stream part. With the
   * client's {@code queryMethod}, a query dialect URL goes out as {@code QUERY} ({@link
   * MediaRequest#of()}).
   *
   * @param url the path or URL
   * @return the response
   */
  public MediaResponse<byte[]> media(String url) {
    return media(url, MediaRequest.of());
  }

  /**
   * {@code GET}, {@code HEAD} or {@code QUERY} any URL or server path.
   *
   * @param url the path or URL
   * @param request the method, range, Accept and headers
   * @return the response
   */
  public MediaResponse<byte[]> media(String url, MediaRequest request) {
    return media(url, request, BodyHandlers.ofByteArray());
  }

  /**
   * {@code GET} or {@code HEAD} any URL or server path, with a body handler ({@code
   * BodyHandlers.ofInputStream()} streams a large body; close the stream).
   *
   * @param url the path or URL
   * @param request the method, range, Accept and headers
   * @param handler the body handler of a 2xx response
   * @param <T> the body type
   * @return the response
   */
  public <T> MediaResponse<T> media(String url, MediaRequest request, BodyHandler<T> handler) {
    return send(core.abs(url), request, handler);
  }

  /**
   * {@code GET} a processed image.
   *
   * @param source the source URL
   * @param options the processing options
   * @return the response
   */
  public MediaResponse<byte[]> image(String source, ProcessingOptions options) {
    return image(source, options, UrlParams.of(), MediaRequest.get());
  }

  /**
   * {@code GET} a processed image with per-URL settings.
   *
   * @param source the source URL
   * @param options the processing options
   * @param params the extension, source encoding and PDF password
   * @return the response
   */
  public MediaResponse<byte[]> image(String source, ProcessingOptions options, UrlParams params) {
    return image(source, options, params, MediaRequest.get());
  }

  /**
   * {@code GET} (or {@code HEAD}) a processed image; the {@code Accept} header drives format
   * negotiation.
   *
   * @param source the source URL
   * @param options the processing options
   * @param params the extension, source encoding and PDF password
   * @param request the method, Accept and headers
   * @return the response
   */
  public MediaResponse<byte[]> image(
      String source, ProcessingOptions options, UrlParams params, MediaRequest request) {
    return media(core.urls.url(source, options, params), request);
  }

  /**
   * {@code GET} a {@code /video} result: video, audio or a waveform.
   *
   * @param source the source video
   * @param options the {@code /video} options
   * @param extension the output container; null for the server's default
   * @return the response
   */
  public MediaResponse<byte[]> video(
      String source, VideoOptions options, @Nullable String extension) {
    return video(source, options, UrlParams.of().withExtension(extension), MediaRequest.get());
  }

  /**
   * {@code GET} (or {@code HEAD}) a {@code /video} result; a range asks for part of it (206).
   *
   * @param source the source video
   * @param options the {@code /video} options
   * @param params the container and source encoding
   * @param request the method, range, Accept and headers
   * @return the response
   */
  public MediaResponse<byte[]> video(
      String source, VideoOptions options, UrlParams params, MediaRequest request) {
    return media(core.urls.videoUrl(source, options, params), request);
  }

  /**
   * {@code GET} an HLS or DASH manifest ({@code /video/...@m3u8} or {@code @mpd}).
   *
   * @param source the source video
   * @param stream the protocol, renditions, segment settings and other options
   * @return the manifest
   */
  public StreamManifest stream(String source, StreamParams stream) {
    String url = core.urls.streamUrl(source, stream);
    String ctype = ClientCore.manifestType(stream.protocol());
    MediaResponse<byte[]> res =
        send(url, MediaRequest.get().withAccept(ctype + ", */*;q=0.5"), BodyHandlers.ofByteArray());
    return new StreamManifest(url, res.contentType().orElse(ctype), ClientCore.text(res.body()));
  }

  /**
   * {@code GET} the playground's {@code api/meta} (needs XI_PLAYGROUND_ENABLED; admin listener by
   * default).
   *
   * @return the decoded JSON object
   */
  public Map<String, @Nullable Object> playgroundMeta() {
    return ClientCore.jsonObject(
        send(
            core.playgroundMeta,
            MediaRequest.get().withAccept("application/json"),
            BodyHandlers.ofByteArray()));
  }

  @Override
  public String toString() {
    return "XtandardClient[" + core.urls + "]";
  }
}
