package io.github.xantiagoma.xtandard.image;

import org.jspecify.annotations.Nullable;

/**
 * A non-2xx response. The nested subclasses narrow it by status, so callers can catch {@link
 * NotFound} or {@link Forbidden} alone.
 *
 * <p>{@link #kind()}, {@link #getMessage()} and {@link #requestId()} come from the server's JSON
 * error envelope; a body that is not the envelope (a proxy's error page, say) gives kind {@code
 * "unknown"} and the body text.
 */
public non-sealed class HttpException extends XtandardException {
  private static final long serialVersionUID = 1L;

  private final int status;
  private final String url;
  private final transient @Nullable ErrorResponse body;

  /**
   * Creates the exception.
   *
   * @param status the HTTP status
   * @param url the request URL
   * @param message the client-safe message
   * @param body the parsed error envelope, if the body was one
   */
  public HttpException(int status, String url, String message, @Nullable ErrorResponse body) {
    super(message, null);
    this.status = status;
    this.url = url;
    this.body = body;
  }

  /**
   * The exception for a status: {@link BadRequest} for 400, {@link Forbidden} for 403, and so on.
   *
   * @param status the HTTP status
   * @param url the request URL
   * @param message the client-safe message
   * @param body the parsed error envelope, if the body was one
   * @return the exception of the most specific class
   */
  public static HttpException of(
      int status, String url, String message, @Nullable ErrorResponse body) {
    return switch (status) {
      case 400 -> new BadRequest(status, url, message, body);
      case 403 -> new Forbidden(status, url, message, body);
      case 404 -> new NotFound(status, url, message, body);
      case 406 -> new NotAcceptable(status, url, message, body);
      case 422 -> new Unprocessable(status, url, message, body);
      case 429 -> new TooManyRequests(status, url, message, body);
      case 502, 504 -> new Gateway(status, url, message, body);
      case 503 -> new ServiceUnavailable(status, url, message, body);
      default ->
          status >= 500
              ? new ServerError(status, url, message, body)
              : new HttpException(status, url, message, body);
    };
  }

  /**
   * The HTTP status.
   *
   * @return the status code
   */
  public int status() {
    return status;
  }

  /**
   * The request URL.
   *
   * @return the URL
   */
  public String url() {
    return url;
  }

  /**
   * The parsed error envelope, when the body was one.
   *
   * @return the envelope, or null
   */
  public @Nullable ErrorResponse body() {
    return body;
  }

  /**
   * The error kind ({@code invalid_url}, {@code unauthorized}, {@code not_found}, ...), or {@code
   * unknown} when the body was not the envelope.
   *
   * @return the kind
   */
  public String kind() {
    ErrorResponse b = body;
    return b == null ? "unknown" : b.error().kind();
  }

  /**
   * The request ID from the envelope, also in the server logs.
   *
   * @return the ID, or null
   */
  public @Nullable String requestId() {
    ErrorResponse b = body;
    return b == null ? null : b.error().requestId();
  }

  /** 400: the URL is invalid (kind {@code invalid_url}). */
  public static final class BadRequest extends HttpException {
    private static final long serialVersionUID = 1L;

    BadRequest(int status, String url, String message, @Nullable ErrorResponse body) {
      super(status, url, message, body);
    }
  }

  /**
   * 403: a bad or missing signature ({@code unauthorized}) or a refused source ({@code forbidden}).
   */
  public static final class Forbidden extends HttpException {
    private static final long serialVersionUID = 1L;

    Forbidden(int status, String url, String message, @Nullable ErrorResponse body) {
      super(status, url, message, body);
    }
  }

  /** 404: the source does not exist ({@code not_found}) or the URL expired ({@code expired}). */
  public static final class NotFound extends HttpException {
    private static final long serialVersionUID = 1L;

    NotFound(int status, String url, String message, @Nullable ErrorResponse body) {
      super(status, url, message, body);
    }
  }

  /** 406: no acceptable output format. */
  public static final class NotAcceptable extends HttpException {
    private static final long serialVersionUID = 1L;

    NotAcceptable(int status, String url, String message, @Nullable ErrorResponse body) {
      super(status, url, message, body);
    }
  }

  /** 422: the source is too large, unsupported, corrupt or over a processing limit. */
  public static final class Unprocessable extends HttpException {
    private static final long serialVersionUID = 1L;

    Unprocessable(int status, String url, String message, @Nullable ErrorResponse body) {
      super(status, url, message, body);
    }
  }

  /** 429: the server is overloaded; retry later. */
  public static final class TooManyRequests extends HttpException {
    private static final long serialVersionUID = 1L;

    TooManyRequests(int status, String url, String message, @Nullable ErrorResponse body) {
      super(status, url, message, body);
    }
  }

  /** 5xx: the server, an upstream source or a timeout failed. */
  public static class ServerError extends HttpException {
    private static final long serialVersionUID = 1L;

    ServerError(int status, String url, String message, @Nullable ErrorResponse body) {
      super(status, url, message, body);
    }
  }

  /** 503: the server is unavailable or draining. */
  public static final class ServiceUnavailable extends ServerError {
    private static final long serialVersionUID = 1L;

    ServiceUnavailable(int status, String url, String message, @Nullable ErrorResponse body) {
      super(status, url, message, body);
    }
  }

  /** 502 and 504: an upstream error or timeout. */
  public static final class Gateway extends ServerError {
    private static final long serialVersionUID = 1L;

    Gateway(int status, String url, String message, @Nullable ErrorResponse body) {
      super(status, url, message, body);
    }
  }
}
