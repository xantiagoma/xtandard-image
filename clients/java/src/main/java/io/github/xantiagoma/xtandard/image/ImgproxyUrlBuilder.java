package io.github.xantiagoma.xtandard.image;

import java.util.ArrayList;
import java.util.List;
import org.jspecify.annotations.Nullable;

/**
 * Builds imgproxy-dialect URLs for an xtandard-image (or imgproxy) server.
 *
 * <pre>
 * {base}[/info]/{signature}/{option}/...[/-/{option}/...]/{source}
 * {base}/video/{signature}/{video option}/.../{source}      (signs "/video" + path)
 * </pre>
 *
 * <p>Sources are base64url (default), {@code plain/} (percent-escaped, {@code @ext}) or {@code
 * enc/} (AES-CBC); base64 and encrypted sources take {@code .ext}.
 *
 * <pre>{@code
 * ImgproxyUrlBuilder img = ImgproxyUrlBuilder.builder()
 *     .baseUrl("https://img.example.com")
 *     .signing(SigningKey.of(keyHex, saltHex))
 *     .build();
 * img.url("s3://bucket/cat.jpg", ProcessingOptions.create().resize("fill", 300, 400).quality(80), "webp");
 * img.image("s3://bucket/cat.jpg").resize("fill", 300, 400).quality(80).extension("webp").build();
 * img.videoUrl("s3://bucket/talk.mov", VideoOptions.create().start(12).duration(8), "webm");
 * img.streamUrl("s3://bucket/talk.mov", StreamParams.hls().withRenditions(360, 720));
 * }</pre>
 *
 * <p>Immutable and thread-safe.
 */
public final class ImgproxyUrlBuilder {
  enum Endpoint {
    PROCESS,
    INFO,
    VIDEO
  }

  private final String baseUrl;
  private final @Nullable SigningKey signing;
  private final String unsignedSignature;
  private final @Nullable EncryptionKey encryption;
  private final SourceEncoding sourceEncoding;
  private final int splitSource;
  private final String separator;
  private final OptionNameStyle optionNames;

  private ImgproxyUrlBuilder(Builder b) {
    this.baseUrl = b.baseUrl;
    this.signing = b.signing;
    this.unsignedSignature = b.unsignedSignature;
    this.encryption = b.encryption;
    this.sourceEncoding = b.sourceEncoding;
    this.splitSource = b.splitSource;
    this.separator = b.separator;
    this.optionNames = b.optionNames;
  }

  /**
   * Starts a builder with the defaults: root-relative URLs, unsigned ({@code insecure}), base64
   * sources, {@code :} separator, short option names.
   *
   * @return a new builder
   */
  public static Builder builder() {
    return new Builder();
  }

  /**
   * A URL builder with every default.
   *
   * @return the URL builder
   */
  public static ImgproxyUrlBuilder create() {
    return new Builder().build();
  }

  /**
   * A builder with this URL builder's settings.
   *
   * @return a new builder
   */
  public Builder toBuilder() {
    Builder b = new Builder();
    b.baseUrl = baseUrl;
    b.signing = signing;
    b.unsignedSignature = unsignedSignature;
    b.encryption = encryption;
    b.sourceEncoding = sourceEncoding;
    b.splitSource = splitSource;
    b.separator = separator;
    b.optionNames = optionNames;
    return b;
  }

  /** Settings of an {@link ImgproxyUrlBuilder}. Not thread-safe. */
  public static final class Builder {
    private String baseUrl = "";
    private @Nullable SigningKey signing;
    private String unsignedSignature = Constants.DEFAULT_UNSIGNED;
    private @Nullable EncryptionKey encryption;
    private SourceEncoding sourceEncoding = SourceEncoding.BASE64;
    private int splitSource;
    private String separator = Constants.DEFAULT_SEPARATOR;
    private OptionNameStyle optionNames = OptionNameStyle.SHORT;

    private Builder() {}

    /**
     * Prepended to every path: the server origin and the dialect's mount prefix, if any. Trailing
     * slashes are dropped. Default {@code ""} (root-relative paths).
     *
     * @param baseUrl the base URL
     * @return this
     */
    public Builder baseUrl(String baseUrl) {
      this.baseUrl = baseUrl;
      return this;
    }

    /**
     * Signs URLs with the imgproxy scheme; without a key the signature segment is the unsigned
     * placeholder.
     *
     * @param signing the key, or null for unsigned URLs
     * @return this
     */
    public Builder signing(@Nullable SigningKey signing) {
      this.signing = signing;
      return this;
    }

    /**
     * The signature segment of unsigned URLs: {@code insecure} (default), {@code unsafe} or {@code
     * _}.
     *
     * @param placeholder the placeholder
     * @return this
     */
    public Builder unsignedSignature(String placeholder) {
      this.unsignedSignature = placeholder;
      return this;
    }

    /**
     * The key for encrypted sources and PDF passwords.
     *
     * @param encryption the key, or null
     * @return this
     */
    public Builder encryption(@Nullable EncryptionKey encryption) {
      this.encryption = encryption;
      return this;
    }

    /**
     * The default source encoding. Default {@link SourceEncoding#BASE64}.
     *
     * @param encoding the encoding
     * @return this
     */
    public Builder sourceEncoding(SourceEncoding encoding) {
      this.sourceEncoding = encoding;
      return this;
    }

    /**
     * Splits base64 and encrypted sources into {@code /}-separated chunks of this many characters.
     *
     * @param chunk the chunk length; 0 (default) does not split
     * @return this
     */
    public Builder splitSource(int chunk) {
      this.splitSource = chunk;
      return this;
    }

    /**
     * The argument separator (XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR). Default {@code :}.
     *
     * @param separator one character, not {@code /}, {@code -} or {@code %}
     * @return this
     */
    public Builder separator(String separator) {
      this.separator = separator;
      return this;
    }

    /**
     * How option names are written. Default {@link OptionNameStyle#SHORT}.
     *
     * @param style the style
     * @return this
     */
    public Builder optionNames(OptionNameStyle style) {
      this.optionNames = style;
      return this;
    }

    /**
     * Builds the URL builder. The separator is checked when a URL is built.
     *
     * @return the URL builder
     */
    public ImgproxyUrlBuilder build() {
      return new ImgproxyUrlBuilder(this);
    }
  }

  // ---- object form ----------------------------------------------------------------------------

  /**
   * A processing URL without options.
   *
   * @param source the source URL ({@code https://}, {@code s3://}, {@code xi://}, ...)
   * @return the URL
   * @throws UrlBuildException when the source is invalid
   */
  public String url(String source) {
    return url(source, List.of(), UrlParams.of());
  }

  /**
   * A processing URL.
   *
   * @param source the source URL
   * @param options the processing options
   * @return the URL
   * @throws UrlBuildException when an option or the source is invalid
   */
  public String url(String source, ProcessingOptions options) {
    return url(source, List.of(options), UrlParams.of());
  }

  /**
   * A processing URL with an output format.
   *
   * @param source the source URL
   * @param options the processing options
   * @param extension the output format ({@code @webp} / {@code .webp}); null for none
   * @return the URL
   * @throws UrlBuildException when an option, the source or the extension is invalid
   */
  public String url(String source, ProcessingOptions options, @Nullable String extension) {
    return url(source, List.of(options), UrlParams.of().withExtension(extension));
  }

  /**
   * A processing URL with per-URL settings.
   *
   * @param source the source URL
   * @param options the processing options
   * @param params the extension, source encoding and PDF password
   * @return the URL
   * @throws UrlBuildException when an option, the source, the extension or a key is invalid
   */
  public String url(String source, ProcessingOptions options, UrlParams params) {
    return url(source, List.of(options), params);
  }

  /**
   * A processing URL with chained pipelines ({@code /-/}); empty pipelines are dropped.
   *
   * @param source the source URL
   * @param pipelines the options of each pipeline, in order
   * @param params the extension, source encoding and PDF password
   * @return the URL
   * @throws UrlBuildException when an option, the source, the extension or a key is invalid
   */
  public String url(String source, List<ProcessingOptions> pipelines, UrlParams params) {
    return build(Endpoint.PROCESS, entriesOf(pipelines), source, params);
  }

  /**
   * An {@code /info} URL without options.
   *
   * @param source the source URL
   * @return the URL
   * @throws UrlBuildException when the source is invalid
   */
  public String infoUrl(String source) {
    return infoUrl(source, InfoOptions.create(), UrlParams.of());
  }

  /**
   * An {@code /info} URL.
   *
   * @param source the source URL
   * @param options the {@code /info} options
   * @return the URL
   * @throws UrlBuildException when an option or the source is invalid
   */
  public String infoUrl(String source, InfoOptions options) {
    return infoUrl(source, options, UrlParams.of());
  }

  /**
   * An {@code /info} URL with per-URL settings ({@code /info} takes no extension).
   *
   * @param source the source URL
   * @param options the {@code /info} options
   * @param params the source encoding and PDF password
   * @return the URL
   * @throws UrlBuildException when an option, the source or a key is invalid, or for an extension
   */
  public String infoUrl(String source, InfoOptions options, UrlParams params) {
    return build(Endpoint.INFO, List.of(options.entries()), source, params);
  }

  /**
   * A {@code /video} URL (a video transformation, audio or a waveform).
   *
   * @param source the source video
   * @param options the {@code /video} options
   * @param extension the output container ({@code webm}, {@code mp4}, {@code mp3}, {@code png},
   *     ...); null for the server's default
   * @return the URL
   * @throws UrlBuildException when an option, the source or the extension is invalid
   */
  public String videoUrl(String source, VideoOptions options, @Nullable String extension) {
    return videoUrl(source, options, UrlParams.of().withExtension(extension));
  }

  /**
   * A {@code /video} URL with an output container.
   *
   * @param source the source video
   * @param options the {@code /video} options
   * @param container the output container
   * @return the URL
   * @throws UrlBuildException when an option or the source is invalid
   */
  public String videoUrl(String source, VideoOptions options, VideoContainer container) {
    return videoUrl(source, options, UrlParams.of().withExtension(container));
  }

  /**
   * A {@code /video} URL with per-URL settings ({@code /video} takes no PDF password).
   *
   * @param source the source video
   * @param options the {@code /video} options
   * @param params the container and source encoding
   * @return the URL
   * @throws UrlBuildException when an option, the source or the extension is invalid, or for a PDF
   *     password
   */
  public String videoUrl(String source, VideoOptions options, UrlParams params) {
    return build(Endpoint.VIDEO, List.of(options.entries()), source, params);
  }

  /**
   * An HLS or DASH manifest URL ({@code /video/...@m3u8} or {@code @mpd}).
   *
   * @param source the source video
   * @param stream the protocol, renditions, segment settings and other options
   * @return the URL
   * @throws UrlBuildException {@link ErrorCode#INVALID_REQUEST} for an unknown protocol, {@code ts}
   *     on DASH, or {@code stream}, {@code segment_duration}, {@code segment_format} or {@code
   *     format} among the options; or an invalid option or source
   */
  public String streamUrl(String source, StreamParams stream) {
    StreamParts parts = StreamParts.of(stream);
    return build(
        Endpoint.VIDEO,
        List.of(parts.entries()),
        source,
        UrlParams.of()
            .withExtension(parts.container())
            .withSourceEncoding(stream.sourceEncoding()));
  }

  // ---- fluent form ----------------------------------------------------------------------------

  /**
   * Starts a fluent processing URL.
   *
   * @param source the source URL
   * @return the chain
   */
  public ImgproxyUrlChain image(String source) {
    return new ImgproxyUrlChain(this, source);
  }

  /**
   * Starts a fluent {@code /info} URL.
   *
   * @param source the source URL
   * @return the chain
   */
  public ImgproxyInfoUrlChain info(String source) {
    return new ImgproxyInfoUrlChain(this, source);
  }

  /**
   * Starts a fluent {@code /video} URL.
   *
   * @param source the source video
   * @return the chain
   */
  public ImgproxyVideoUrlChain video(String source) {
    return new ImgproxyVideoUrlChain(this, source);
  }

  // ---- building -------------------------------------------------------------------------------

  static List<List<OptionEntry>> entriesOf(List<? extends OptionList<?>> pipelines) {
    List<List<OptionEntry>> out = new ArrayList<>(pipelines.size());
    for (OptionList<?> p : pipelines) {
      out.add(p.entries());
    }
    return out;
  }

  private String build(
      Endpoint endpoint, List<List<OptionEntry>> pipelines, String source, UrlParams params) {
    return build(
        endpoint,
        pipelines,
        source,
        params.sourceEncoding(),
        params.extension(),
        params.pdfPassword());
  }

  String build(
      Endpoint endpoint,
      List<List<OptionEntry>> pipelines,
      String source,
      @Nullable SourceEncoding encoding,
      @Nullable String ext,
      @Nullable String pdfPassword) {
    String sep = Text.checkSeparator(separator, Constants.FORBIDDEN_SEPARATORS);
    Catalogue cat =
        endpoint == Endpoint.INFO
            ? Catalogue.INFO
            : endpoint == Endpoint.VIDEO ? Catalogue.VIDEO : Catalogue.PROCESSING;
    List<List<OptionEntry>> nonEmpty = new ArrayList<>();
    for (List<OptionEntry> p : pipelines) {
      if (!p.isEmpty()) {
        nonEmpty.add(p);
      }
    }
    String what = endpoint == Endpoint.INFO ? "/info" : "/video";
    if (endpoint != Endpoint.PROCESS && nonEmpty.size() > 1) {
      throw new UrlBuildException(
          ErrorCode.INVALID_REQUEST, what + " URLs have no chained pipelines");
    }
    if (endpoint == Endpoint.VIDEO && pdfPassword != null) {
      throw new UrlBuildException(ErrorCode.INVALID_REQUEST, "/video URLs take no pdf_password");
    }
    if (endpoint == Endpoint.INFO && ext != null && !ext.isEmpty()) {
      throw new UrlBuildException(ErrorCode.INVALID_REQUEST, "/info URLs take no extension");
    }
    List<String> segments = new ArrayList<>();
    if (pdfPassword != null) {
      String token = passwordToken(encryption, signing, pdfPassword);
      String name =
          optionNames == OptionNameStyle.CANONICAL
              ? Constants.PDF_PASSWORD_NAME
              : Constants.PDF_PASSWORD_SHORT;
      segments.add(name + sep + token);
    }
    for (int i = 0; i < nonEmpty.size(); i++) {
      if (i > 0) {
        segments.add(Constants.PIPELINE_SEPARATOR);
      }
      for (OptionEntry e : nonEmpty.get(i)) {
        segments.add(cat.format(e, sep, optionNames));
      }
    }
    segments.add(sourceSegment(source, encoding == null ? sourceEncoding : encoding, ext));
    String path = "/" + String.join("/", segments);
    String base = Signing.stripTrailingSlashes(baseUrl);
    SigningKey key = signing;
    if (endpoint == Endpoint.VIDEO) {
      String sig = key == null ? unsignedSignature : Signing.sign("/video" + path, key);
      return base + "/video/" + sig + path;
    }
    String signed =
        key == null ? "/" + unsignedSignature + path : Signing.signImgproxyPath(path, key);
    return base + (endpoint == Endpoint.INFO ? "/info" : "") + signed;
  }

  private String sourceSegment(String source, SourceEncoding encoding, @Nullable String ext) {
    Text.checkText(source, ErrorCode.INVALID_SOURCE, "source");
    String e = ext == null || ext.isEmpty() ? null : Text.checkExtension(ext);
    return switch (encoding) {
      case PLAIN -> "plain/" + Text.escapePlain(source) + (e == null ? "" : "@" + e);
      case ENCRYPTED -> {
        EncryptionKey key = encryption;
        if (key == null) {
          throw new UrlBuildException(
              ErrorCode.MISSING_ENCRYPTION, "encrypted sources need an encryption key");
        }
        yield "enc/" + split(Encryption.encryptSource(source, key)) + (e == null ? "" : "." + e);
      }
      case BASE64 -> split(Text.b64url(Text.utf8(source))) + (e == null ? "" : "." + e);
    };
  }

  private String split(String token) {
    int n = splitSource;
    if (n <= 0 || token.length() <= n) {
      return token;
    }
    StringBuilder b = new StringBuilder(token.length() + token.length() / n);
    for (int i = 0; i < token.length(); i += n) {
      if (i > 0) {
        b.append('/');
      }
      b.append(token, i, Math.min(token.length(), i + n));
    }
    return b.toString();
  }

  /** The pdf_password token, checking that both keys are present. */
  static String passwordToken(
      @Nullable EncryptionKey encryption, @Nullable SigningKey signing, String password) {
    if (encryption == null) {
      throw new UrlBuildException(
          ErrorCode.MISSING_ENCRYPTION, "pdf_password needs an encryption key");
    }
    if (signing == null) {
      throw new UrlBuildException(
          ErrorCode.MISSING_SIGNING,
          "pdf_password needs signing: the server refuses it on unsigned URLs");
    }
    return Encryption.encryptPdfPassword(password, encryption);
  }

  /** The base URL without trailing slashes. */
  String baseUrl() {
    return Signing.stripTrailingSlashes(baseUrl);
  }

  @Override
  public String toString() {
    return "ImgproxyUrlBuilder[baseUrl="
        + baseUrl
        + ", signed="
        + (signing != null)
        + ", encryption="
        + (encryption != null)
        + ", sourceEncoding="
        + sourceEncoding
        + ", separator="
        + separator
        + ", optionNames="
        + optionNames
        + "]";
  }
}
