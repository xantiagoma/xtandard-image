package io.github.xantiagoma.xtandard.image;

/**
 * The {@code /info} options, in URL order: one typed method per option name and alias ({@link
 * InfoMethods}), or {@link #option(String, Object...)} for any name.
 *
 * <pre>{@code
 * InfoOptions.create().size(true).format(true).dimensions(true)
 * }</pre>
 */
public final class InfoOptions extends OptionList<InfoOptions> implements InfoMethods<InfoOptions> {

  private InfoOptions() {}

  /**
   * Starts an empty options list.
   *
   * @return a new list
   */
  public static InfoOptions create() {
    return new InfoOptions();
  }

  @Override
  InfoOptions self() {
    return this;
  }
}
