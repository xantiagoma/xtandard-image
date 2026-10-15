package io.github.xantiagoma.xtandard.image;

/** A URL, token or header cannot be built from the given input; {@link #code()} says why. */
public final class UrlBuildException extends XtandardException {
  private static final long serialVersionUID = 1L;

  private final ErrorCode code;

  /**
   * Creates the exception.
   *
   * @param code the language-neutral error code
   * @param message what is wrong
   */
  public UrlBuildException(ErrorCode code, String message) {
    super(message, null);
    this.code = code;
  }

  /**
   * The language-neutral error code (see {@code clients/README.md}).
   *
   * @return the code
   */
  public ErrorCode code() {
    return code;
  }
}
