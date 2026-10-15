package io.github.xantiagoma.xtandard.image;

import org.jspecify.annotations.Nullable;

/**
 * Base class of every exception this library throws: {@link UrlBuildException} (bad input to a
 * builder), {@link HttpException} (a non-2xx response) and {@link TransportException} (no
 * response).
 */
public abstract sealed class XtandardException extends RuntimeException
    permits UrlBuildException, HttpException, TransportException {
  private static final long serialVersionUID = 1L;

  XtandardException(String message, @Nullable Throwable cause) {
    super(message, cause);
  }
}
