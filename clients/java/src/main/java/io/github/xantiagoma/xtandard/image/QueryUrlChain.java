package io.github.xantiagoma.xtandard.image;

import org.jspecify.annotations.Nullable;

/**
 * A fluent {@code /q/?url=...} URL: the option methods write {@code o=}, {@link #param} a native
 * key. Start it with {@link QueryUrlBuilder#image(String)}.
 */
public final class QueryUrlChain extends UrlChain<QueryUrlChain>
    implements ProcessingMethods<QueryUrlChain> {
  private final QueryUrlBuilder builder;
  private final QueryParams params = QueryParams.create();

  QueryUrlChain(QueryUrlBuilder builder, String source) {
    super(source);
    this.builder = builder;
  }

  @Override
  QueryUrlChain self() {
    return this;
  }

  /**
   * Appends a native key ({@code w}, {@code fit}, {@code g}, ...), written before {@code o=}.
   *
   * @param key the key
   * @param value a string, number or boolean; null skips the key
   * @return this
   */
  public QueryUrlChain param(String key, @Nullable Object value) {
    params.param(key, value);
    return this;
  }

  /**
   * Appends every native key of a parameter list, in order.
   *
   * @param params the keys
   * @return this
   */
  public QueryUrlChain params(QueryParams params) {
    for (var e : params.entries()) {
      this.params.param(e.getKey(), e.getValue());
    }
    return this;
  }

  /**
   * Appends every option of an options list, in order.
   *
   * @param options the options
   * @return this
   */
  public QueryUrlChain options(ProcessingOptions options) {
    return addAll(options);
  }

  /**
   * Starts the next chained pipeline ({@code /-/} inside {@code o=}).
   *
   * @return this
   */
  public QueryUrlChain pipeline() {
    return nextPipeline();
  }

  /**
   * Sets the output format ({@code format=}).
   *
   * @param extension the format
   * @return this
   */
  public QueryUrlChain extension(String extension) {
    this.ext = extension;
    return this;
  }

  /**
   * Sets the output format.
   *
   * @param format the format
   * @return this
   */
  public QueryUrlChain extension(ImageFormat format) {
    return extension(format.value());
  }

  /**
   * Opens a password-protected PDF ({@code pdp=}; needs both keys).
   *
   * @param password the password
   * @return this
   */
  public QueryUrlChain withPdfPassword(String password) {
    this.pdfPassword = password;
    return this;
  }

  @Override
  public String build() {
    return builder.image(source, params, pipelines, ext, pdfPassword);
  }
}
