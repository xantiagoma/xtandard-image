package io.github.xantiagoma.xtandard.image;

/**
 * The processing options of one pipeline, in URL order: one typed method per option name and alias
 * ({@link ProcessingMethods}), or {@link #option(String, Object...)} for any name.
 *
 * <pre>{@code
 * ProcessingOptions.create().resize("fill", 300, 400).gravity("sm").quality(80)
 * }</pre>
 */
public final class ProcessingOptions extends OptionList<ProcessingOptions>
    implements ProcessingMethods<ProcessingOptions> {

  private ProcessingOptions() {}

  /**
   * Starts an empty options list.
   *
   * @return a new list
   */
  public static ProcessingOptions create() {
    return new ProcessingOptions();
  }

  @Override
  ProcessingOptions self() {
    return this;
  }
}
