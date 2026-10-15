package io.github.xantiagoma.xtandard.image;

import java.util.ArrayList;
import java.util.List;

/**
 * A fluent {@code /q/video} URL: one method per {@code /video} option name and alias. Start it with
 * {@link QueryUrlBuilder#video(String)}.
 */
public final class QueryVideoUrlChain extends UrlChain<QueryVideoUrlChain>
    implements VideoMethods<QueryVideoUrlChain> {
  private final QueryUrlBuilder builder;

  QueryVideoUrlChain(QueryUrlBuilder builder, String source) {
    super(source);
    this.builder = builder;
  }

  @Override
  QueryVideoUrlChain self() {
    return this;
  }

  /**
   * Appends every option of an options list, in order.
   *
   * @param options the options
   * @return this
   */
  public QueryVideoUrlChain options(VideoOptions options) {
    return addAll(options);
  }

  /**
   * Sets the output container ({@code format=}).
   *
   * @param extension the container
   * @return this
   */
  public QueryVideoUrlChain extension(String extension) {
    this.ext = extension;
    return this;
  }

  /**
   * Sets the output container.
   *
   * @param container the container
   * @return this
   */
  public QueryVideoUrlChain extension(VideoContainer container) {
    return extension(container.value());
  }

  @Override
  public String build() {
    List<OptionEntry> all = new ArrayList<>();
    for (List<OptionEntry> p : pipelines) {
      all.addAll(p);
    }
    return builder.video(source, all, ext);
  }
}
