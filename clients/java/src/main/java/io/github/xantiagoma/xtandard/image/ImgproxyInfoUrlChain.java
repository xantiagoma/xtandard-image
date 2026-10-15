package io.github.xantiagoma.xtandard.image;

/**
 * A fluent {@code /info} URL: one method per {@code /info} option name and alias. Start it with
 * {@link ImgproxyUrlBuilder#info(String)}.
 */
public final class ImgproxyInfoUrlChain extends UrlChain<ImgproxyInfoUrlChain>
    implements InfoMethods<ImgproxyInfoUrlChain> {
  private final ImgproxyUrlBuilder builder;

  ImgproxyInfoUrlChain(ImgproxyUrlBuilder builder, String source) {
    super(source);
    this.builder = builder;
  }

  @Override
  ImgproxyInfoUrlChain self() {
    return this;
  }

  /**
   * Appends every option of an options list, in order.
   *
   * @param options the options
   * @return this
   */
  public ImgproxyInfoUrlChain options(InfoOptions options) {
    return addAll(options);
  }

  /**
   * Overrides the source encoding.
   *
   * @param encoding the encoding
   * @return this
   */
  public ImgproxyInfoUrlChain encoding(SourceEncoding encoding) {
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
  public ImgproxyInfoUrlChain withPdfPassword(String password) {
    this.pdfPassword = password;
    return this;
  }

  @Override
  public String build() {
    return builder.build(
        ImgproxyUrlBuilder.Endpoint.INFO, pipelines, source, encoding, null, pdfPassword);
  }
}
