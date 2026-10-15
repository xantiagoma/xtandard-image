package io.github.xantiagoma.xtandard.image;

import java.util.ArrayList;
import java.util.List;
import org.jspecify.annotations.Nullable;

/**
 * A fluent URL: what every chain shares. The per-option methods come from {@link
 * ProcessingMethods}, {@link InfoMethods} or {@link VideoMethods}; {@link #build()} (or {@link
 * #toString()}) writes the URL. Mutable and not thread-safe.
 *
 * @param <S> the concrete chain type
 */
public abstract sealed class UrlChain<S extends UrlChain<S>>
    permits ImgproxyUrlChain,
        ImgproxyInfoUrlChain,
        ImgproxyVideoUrlChain,
        QueryUrlChain,
        QueryVideoUrlChain {
  final String source;
  final List<List<OptionEntry>> pipelines = new ArrayList<>();
  @Nullable SourceEncoding encoding;
  @Nullable String ext;
  @Nullable String pdfPassword;

  UrlChain(String source) {
    this.source = source;
    pipelines.add(new ArrayList<>());
  }

  abstract S self();

  /**
   * Appends one option occurrence (what the per-option methods call).
   *
   * @param name an option name or alias
   * @param args the arguments: strings, numbers, booleans or null (an empty argument)
   * @return this
   */
  public S option(String name, @Nullable Object @Nullable ... args) {
    pipelines.get(pipelines.size() - 1).add(OptionEntry.of(name, args));
    return self();
  }

  S addAll(OptionList<?> options) {
    pipelines.get(pipelines.size() - 1).addAll(options.entries());
    return self();
  }

  S nextPipeline() {
    pipelines.add(new ArrayList<>());
    return self();
  }

  /**
   * Builds the URL.
   *
   * @return the URL
   * @throws UrlBuildException when an option, the source, the extension or a key is invalid
   */
  public abstract String build();

  /**
   * Builds the URL ({@link #build()}).
   *
   * @return the URL
   */
  @Override
  public String toString() {
    return build();
  }
}
