package io.github.xantiagoma.xtandard.image;

/**
 * A fluent {@code /video} URL: video transformations, audio and waveforms. Start it with {@link
 * ImgproxyUrlBuilder#video(String)}.
 */
public final class ImgproxyVideoUrlChain extends UrlChain<ImgproxyVideoUrlChain>
    implements VideoMethods<ImgproxyVideoUrlChain> {
  private final ImgproxyUrlBuilder builder;

  ImgproxyVideoUrlChain(ImgproxyUrlBuilder builder, String source) {
    super(source);
    this.builder = builder;
  }

  @Override
  ImgproxyVideoUrlChain self() {
    return this;
  }

  /**
   * Appends every option of an options list, in order.
   *
   * @param options the options
   * @return this
   */
  public ImgproxyVideoUrlChain options(VideoOptions options) {
    return addAll(options);
  }

  /**
   * Sets the output container ({@code webm}, {@code mp4}, {@code mp3}, {@code png}, ...).
   *
   * @param extension the container
   * @return this
   */
  public ImgproxyVideoUrlChain extension(String extension) {
    this.ext = extension;
    return this;
  }

  /**
   * Sets the output container.
   *
   * @param container the container
   * @return this
   */
  public ImgproxyVideoUrlChain extension(VideoContainer container) {
    return extension(container.value());
  }

  /**
   * Overrides the source encoding.
   *
   * @param encoding the encoding
   * @return this
   */
  public ImgproxyVideoUrlChain encoding(SourceEncoding encoding) {
    this.encoding = encoding;
    return this;
  }

  @Override
  public String build() {
    return builder.build(ImgproxyUrlBuilder.Endpoint.VIDEO, pipelines, source, encoding, ext, null);
  }
}
