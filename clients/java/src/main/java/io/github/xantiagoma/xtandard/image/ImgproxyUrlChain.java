package io.github.xantiagoma.xtandard.image;

/**
 * A fluent processing URL: one method per option name and alias, {@link #pipeline()} for {@code
 * /-/}. Start it with {@link ImgproxyUrlBuilder#image(String)}.
 */
public final class ImgproxyUrlChain extends UrlChain<ImgproxyUrlChain>
    implements ProcessingMethods<ImgproxyUrlChain> {
  private final ImgproxyUrlBuilder builder;

  ImgproxyUrlChain(ImgproxyUrlBuilder builder, String source) {
    super(source);
    this.builder = builder;
  }

  @Override
  ImgproxyUrlChain self() {
    return this;
  }

  /**
   * Appends every option of an options list, in order.
   *
   * @param options the options
   * @return this
   */
  public ImgproxyUrlChain options(ProcessingOptions options) {
    return addAll(options);
  }

  /**
   * Starts the next chained pipeline ({@code /-/}).
   *
   * @return this
   */
  public ImgproxyUrlChain pipeline() {
    return nextPipeline();
  }

  /**
   * Sets the output format ({@code @webp} / {@code .webp}).
   *
   * @param extension the extension
   * @return this
   */
  public ImgproxyUrlChain extension(String extension) {
    this.ext = extension;
    return this;
  }

  /**
   * Sets the output format.
   *
   * @param format the format
   * @return this
   */
  public ImgproxyUrlChain extension(ImageFormat format) {
    return extension(format.value());
  }

  /**
   * Overrides the source encoding.
   *
   * @param encoding the encoding
   * @return this
   */
  public ImgproxyUrlChain encoding(SourceEncoding encoding) {
    this.encoding = encoding;
    return this;
  }

  /**
   * Opens a password-protected PDF: the plain-text password is encrypted into {@code pdp} (needs
   * both keys).
   *
   * @param password the password
   * @return this
   */
  public ImgproxyUrlChain withPdfPassword(String password) {
    this.pdfPassword = password;
    return this;
  }

  @Override
  public String build() {
    return builder.build(
        ImgproxyUrlBuilder.Endpoint.PROCESS, pipelines, source, encoding, ext, pdfPassword);
  }
}
