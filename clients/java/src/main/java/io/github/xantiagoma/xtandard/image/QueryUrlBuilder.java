package io.github.xantiagoma.xtandard.image;

import java.util.AbstractMap;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;
import org.jspecify.annotations.Nullable;

/**
 * Builds query dialect URLs (the server needs {@code XI_DIALECTS_QUERY_ENABLED=true}), mounted at
 * {@code /q}.
 *
 * <pre>
 * {base}{mount}/?url=&lt;source&gt;&amp;&lt;native key&gt;=...&amp;o=&lt;imgproxy options&gt;[&amp;xi_sig=...]
 * {base}{mount}/video?url=&lt;source&gt;&amp;&lt;video option&gt;=&lt;arg,arg&gt;...[&amp;xi_sig=...]
 * {base}{mount}/&lt;imaginary endpoint&gt;?url=&lt;source&gt;&amp;&lt;param&gt;=...[&amp;xi_sig=...]
 * </pre>
 *
 * <p>With a signing key every URL ends in an {@code xi_sig} over the path after the mount and the
 * query:
 *
 * <pre>{@code
 * QueryUrlBuilder q = QueryUrlBuilder.builder().baseUrl("https://img.example.com").signing(key).build();
 * q.url("s3://bucket/cat.jpg", QueryParams.create().w(300).f("webp"), ProcessingOptions.create().bl(2));
 * // https://img.example.com/q/?url=s3://bucket/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=...
 * }</pre>
 *
 * <p>Immutable and thread-safe.
 */
public final class QueryUrlBuilder {
  private static final Pattern ENDPOINT = Pattern.compile("[a-z]+");

  private final String baseUrl;
  private final String mount;
  private final @Nullable SigningKey signing;
  private final @Nullable EncryptionKey encryption;
  private final String separator;
  private final OptionNameStyle optionNames;

  private QueryUrlBuilder(Builder b) {
    this.baseUrl = b.baseUrl;
    this.mount = b.mount;
    this.signing = b.signing;
    this.encryption = b.encryption;
    this.separator = b.separator;
    this.optionNames = b.optionNames;
  }

  /**
   * Starts a builder with the defaults: root-relative URLs, mount {@code /q}, unsigned, {@code :}
   * separator, short option names.
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
  public static QueryUrlBuilder create() {
    return new Builder().build();
  }

  /** Settings of a {@link QueryUrlBuilder}. Not thread-safe. */
  public static final class Builder {
    private String baseUrl = "";
    private String mount = Constants.QUERY_MOUNT;
    private @Nullable SigningKey signing;
    private @Nullable EncryptionKey encryption;
    private String separator = Constants.DEFAULT_SEPARATOR;
    private OptionNameStyle optionNames = OptionNameStyle.SHORT;

    private Builder() {}

    /**
     * The server origin (and any path before the mount). Default {@code ""}.
     *
     * @param baseUrl the base URL
     * @return this
     */
    public Builder baseUrl(String baseUrl) {
      this.baseUrl = baseUrl;
      return this;
    }

    /**
     * The query dialect's mount prefix (XI_DIALECTS_QUERY_PREFIX). Default {@code /q}.
     *
     * @param mount the mount
     * @return this
     */
    public Builder mount(String mount) {
      this.mount = mount;
      return this;
    }

    /**
     * Signs with {@code xi_sig} (the imgproxy key and salt); without a key URLs are unsigned.
     *
     * @param signing the key, or null
     * @return this
     */
    public Builder signing(@Nullable SigningKey signing) {
      this.signing = signing;
      return this;
    }

    /**
     * The key for PDF passwords (XI_ENCRYPTION_KEYS).
     *
     * @param encryption the key, or null
     * @return this
     */
    public Builder encryption(@Nullable EncryptionKey encryption) {
      this.encryption = encryption;
      return this;
    }

    /**
     * The argument separator inside {@code o=}. Default {@code :}.
     *
     * @param separator one character, not {@code /}, {@code -}, {@code %}, {@code &} or {@code =}
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
     * Builds the URL builder.
     *
     * @return the URL builder
     */
    public QueryUrlBuilder build() {
      return new QueryUrlBuilder(this);
    }
  }

  // ---- object form ----------------------------------------------------------------------------

  /**
   * A native {@code /q/?url=...} image URL with native keys only.
   *
   * @param source the source URL
   * @param params the native keys ({@code w}, {@code fit}, {@code g}, {@code f}, ...)
   * @return the URL
   * @throws UrlBuildException when the source or a value is invalid
   */
  public String url(String source, QueryParams params) {
    return url(source, params, List.of(), UrlParams.of());
  }

  /**
   * A native {@code /q/?url=...} image URL: native keys, then imgproxy options as {@code o=}.
   *
   * @param source the source URL
   * @param params the native keys
   * @param options imgproxy processing options, written into {@code o=}
   * @return the URL
   * @throws UrlBuildException when an option, the source or a value is invalid
   */
  public String url(String source, QueryParams params, ProcessingOptions options) {
    return url(source, params, List.of(options), UrlParams.of());
  }

  /**
   * A native {@code /q/?url=...} image URL with chained pipelines in {@code o=} and per-URL
   * settings ({@code format=} and {@code pdp=}; the query dialect has no source encodings).
   *
   * @param source the source URL
   * @param params the native keys, or null for none
   * @param pipelines imgproxy processing options per pipeline
   * @param urlParams the extension and PDF password
   * @return the URL
   * @throws UrlBuildException when an option, the source, the extension or a key is invalid
   */
  public String url(
      String source,
      @Nullable QueryParams params,
      List<ProcessingOptions> pipelines,
      UrlParams urlParams) {
    rejectEncoding(urlParams.sourceEncoding());
    return image(
        source,
        params,
        ImgproxyUrlBuilder.entriesOf(pipelines),
        urlParams.extension(),
        urlParams.pdfPassword());
  }

  /**
   * A {@code /q/video} URL: every video option is a key, its arguments joined by {@code ,}; {@code
   * crop} and {@code flip} are written {@code c} and {@code fl}.
   *
   * @param source the source video
   * @param options the {@code /video} options
   * @param extension the output container; null for the server's default
   * @return the URL
   * @throws UrlBuildException when an option, the source or the extension is invalid
   */
  public String videoUrl(String source, VideoOptions options, @Nullable String extension) {
    return video(source, options.entries(), extension);
  }

  /**
   * A {@code /q/video} URL from several option lists, written in order (a key may repeat; later
   * wins argument by argument).
   *
   * @param source the source video
   * @param options the {@code /video} option lists
   * @param extension the output container; null for the server's default
   * @return the URL
   * @throws UrlBuildException when an option, the source or the extension is invalid
   */
  public String videoUrl(String source, List<VideoOptions> options, @Nullable String extension) {
    List<OptionEntry> all = new ArrayList<>();
    for (VideoOptions o : options) {
      all.addAll(o.entries());
    }
    return video(source, all, extension);
  }

  /**
   * A {@code /q/video} HLS or DASH manifest URL ({@code format=m3u8|mpd}).
   *
   * @param source the source video
   * @param stream the protocol, renditions, segment settings and other options
   * @return the URL
   * @throws UrlBuildException as {@link ImgproxyUrlBuilder#streamUrl(String, StreamParams)}
   */
  public String streamUrl(String source, StreamParams stream) {
    rejectEncoding(stream.sourceEncoding());
    StreamParts parts = StreamParts.of(stream);
    return video(source, parts.entries(), parts.container());
  }

  /**
   * An imaginary endpoint URL ({@code /q/resize?url=...&width=300}).
   *
   * @param endpoint the endpoint
   * @param source the source URL
   * @param params the imaginary parameters
   * @return the URL
   * @throws UrlBuildException when the source or a value is invalid
   */
  public String imaginaryUrl(ImaginaryEndpoint endpoint, String source, ImaginaryParams params) {
    return imaginaryUrl(endpoint.value(), source, params);
  }

  /**
   * An imaginary endpoint URL for an endpoint given by name.
   *
   * @param endpoint lower-case letters, not {@code video}
   * @param source the source URL
   * @param params the imaginary parameters, or null for none
   * @return the URL
   * @throws UrlBuildException {@link ErrorCode#INVALID_ENDPOINT} for a bad endpoint; or an invalid
   *     source or value
   */
  public String imaginaryUrl(String endpoint, String source, @Nullable ImaginaryParams params) {
    if (!ENDPOINT.matcher(endpoint).matches() || endpoint.equals("video")) {
      throw new UrlBuildException(
          ErrorCode.INVALID_ENDPOINT, "invalid imaginary endpoint \"" + endpoint + "\"");
    }
    List<Map.Entry<String, String>> pairs = new ArrayList<>();
    pairs.add(pair("url", Text.checkText(source, ErrorCode.INVALID_SOURCE, "source")));
    if (params != null) {
      pairs.addAll(params.formatted());
    }
    return finish("/" + endpoint, pairs);
  }

  // ---- fluent form ----------------------------------------------------------------------------

  /**
   * Starts a fluent {@code /q/?url=...} URL: the option methods write {@code o=}, {@link
   * QueryUrlChain#param} a native key.
   *
   * @param source the source URL
   * @return the chain
   */
  public QueryUrlChain image(String source) {
    return new QueryUrlChain(this, source);
  }

  /**
   * Starts a fluent {@code /q/video} URL.
   *
   * @param source the source video
   * @return the chain
   */
  public QueryVideoUrlChain video(String source) {
    return new QueryVideoUrlChain(this, source);
  }

  // ---- building -------------------------------------------------------------------------------

  private static void rejectEncoding(@Nullable SourceEncoding encoding) {
    if (encoding != null) {
      throw new UrlBuildException(
          ErrorCode.INVALID_REQUEST,
          "query URLs carry the source as url=, with no source encoding");
    }
  }

  static Map.Entry<String, String> pair(String k, String v) {
    return new AbstractMap.SimpleImmutableEntry<>(k, v);
  }

  String image(
      String source,
      @Nullable QueryParams params,
      List<List<OptionEntry>> pipelines,
      @Nullable String ext,
      @Nullable String pdfPassword) {
    String sep = Text.checkSeparator(separator, Constants.FORBIDDEN_QUERY_SEPARATORS);
    List<Map.Entry<String, String>> pairs = new ArrayList<>();
    pairs.add(pair("url", Text.checkText(source, ErrorCode.INVALID_SOURCE, "source")));
    if (params != null) {
      pairs.addAll(params.formatted());
    }
    List<String> written = new ArrayList<>();
    for (List<OptionEntry> p : pipelines) {
      if (p.isEmpty()) {
        continue;
      }
      List<String> segs = new ArrayList<>();
      for (OptionEntry e : p) {
        segs.add(Catalogue.PROCESSING.format(e, sep, optionNames));
      }
      written.add(String.join("/", segs));
    }
    if (!written.isEmpty()) {
      pairs.add(pair("o", String.join("/" + Constants.PIPELINE_SEPARATOR + "/", written)));
    }
    if (ext != null && !ext.isEmpty()) {
      pairs.add(pair("format", Text.checkExtension(ext)));
    }
    if (pdfPassword != null) {
      String token = ImgproxyUrlBuilder.passwordToken(encryption, signing, pdfPassword);
      pairs.add(
          pair(
              optionNames == OptionNameStyle.CANONICAL
                  ? Constants.PDF_PASSWORD_NAME
                  : Constants.PDF_PASSWORD_SHORT,
              token));
    }
    return finish("/", pairs);
  }

  String video(String source, List<OptionEntry> entries, @Nullable String ext) {
    List<Map.Entry<String, String>> pairs = new ArrayList<>();
    pairs.add(pair("url", Text.checkText(source, ErrorCode.INVALID_SOURCE, "source")));
    for (OptionEntry e : entries) {
      String key = Catalogue.VIDEO.written(e.name(), optionNames);
      if (e.args().isEmpty()) {
        throw new UrlBuildException(
            ErrorCode.INVALID_ARGUMENT,
            "option " + e.name() + ": at least one argument is required");
      }
      key = Constants.NATIVE_VIDEO_KEYS.getOrDefault(key, key);
      List<String> values = new ArrayList<>();
      for (int i = 0; i < e.args().size(); i++) {
        String where = Catalogue.where(e, i);
        String s = Catalogue.formatArg(e.args().get(i), ",", where);
        if (s.indexOf(':') >= 0) {
          throw new UrlBuildException(
              ErrorCode.INVALID_ARGUMENT, where + ": \"" + s + "\" contains ':'");
        }
        values.add(s);
      }
      pairs.add(pair(key, String.join(",", values)));
    }
    if (ext != null && !ext.isEmpty()) {
      pairs.add(pair("format", Text.checkExtension(ext)));
    }
    return finish("/video", pairs);
  }

  private String finish(String path, List<Map.Entry<String, String>> pairs) {
    String base = Signing.stripTrailingSlashes(baseUrl);
    String m = Signing.stripTrailingSlashes(mount);
    StringBuilder q = new StringBuilder();
    for (Map.Entry<String, String> p : pairs) {
      if (q.length() > 0) {
        q.append('&');
      }
      q.append(Text.escapeQuery(p.getKey())).append('=').append(Text.escapeQuery(p.getValue()));
    }
    String query = q.toString();
    SigningKey key = signing;
    if (key != null) {
      query = Signing.signXiQuery(path, query, key);
    }
    return base + m + path + "?" + query;
  }

  @Override
  public String toString() {
    return "QueryUrlBuilder[baseUrl="
        + baseUrl
        + ", mount="
        + mount
        + ", signed="
        + (signing != null)
        + ", separator="
        + separator
        + ", optionNames="
        + optionNames
        + "]";
  }
}
