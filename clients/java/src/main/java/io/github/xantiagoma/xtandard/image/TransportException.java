package io.github.xantiagoma.xtandard.image;

import org.jspecify.annotations.Nullable;

/**
 * The request got no usable response: a connection, TLS or timeout failure (the cause is the {@link
 * java.io.IOException}), an interrupted call, or a body that is not the JSON it should be.
 */
public final class TransportException extends XtandardException {
  private static final long serialVersionUID = 1L;

  /**
   * Creates the exception.
   *
   * @param message what failed
   * @param cause the underlying exception, if any
   */
  public TransportException(String message, @Nullable Throwable cause) {
    super(message, cause);
  }
}
