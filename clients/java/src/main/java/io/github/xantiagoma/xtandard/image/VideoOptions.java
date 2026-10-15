package io.github.xantiagoma.xtandard.image;

/**
 * The {@code /video} options (transformations, audio, waveforms), in URL order: one typed method
 * per option name and alias ({@link VideoMethods}), or {@link #option(String, Object...)} for any
 * name.
 *
 * <pre>{@code
 * VideoOptions.create().resize("fill", 640, 360).start(12).duration(8)
 * }</pre>
 */
public final class VideoOptions extends OptionList<VideoOptions>
    implements VideoMethods<VideoOptions> {

  private VideoOptions() {}

  /**
   * Starts an empty options list.
   *
   * @return a new list
   */
  public static VideoOptions create() {
    return new VideoOptions();
  }

  @Override
  VideoOptions self() {
    return this;
  }
}
