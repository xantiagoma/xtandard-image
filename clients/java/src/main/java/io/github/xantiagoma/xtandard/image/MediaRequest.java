package io.github.xantiagoma.xtandard.image;

import java.util.LinkedHashMap;
import java.util.Map;
import org.jspecify.annotations.Nullable;

/**
 * How a media request is sent: the method ({@code GET}, {@code HEAD} or {@code QUERY}), a byte
 * range, the {@code Accept} header (which drives format negotiation) and extra headers. Immutable;
 * start from {@link #get()}, {@link #head()}, {@link #query()} or {@link #of()}.
 *
 * @param method {@code GET}, {@code HEAD}, {@code QUERY}, or null for the client's default ({@code
 *     GET}, or {@code QUERY} for a query dialect URL when the client's {@code queryMethod} is on)
 * @param range a byte range, or null for the whole body
 * @param accept the {@code Accept} header
 * @param headers extra headers, sent after the client's own
 */
public record MediaRequest(
    @Nullable String method,
    @Nullable ByteRange range,
    String accept,
    Map<String, String> headers) {

  /**
   * Copies the headers.
   *
   * @param method the method, or null for the client's default
   * @param range the range
   * @param accept the Accept header
   * @param headers extra headers
   */
  public MediaRequest {
    if (method != null
        && !method.equals("GET")
        && !method.equals("HEAD")
        && !method.equals("QUERY")) {
      throw new IllegalArgumentException("method must be GET, HEAD or QUERY, not " + method);
    }
    headers = Map.copyOf(headers);
  }

  /**
   * The client's default method, the whole body, accepting anything: {@code GET}, or {@code QUERY}
   * for a URL under the query dialect mount when the client's {@code queryMethod} is on. {@link
   * XtandardClient#media(String)} uses it.
   *
   * @return the request settings
   */
  public static MediaRequest of() {
    return new MediaRequest(null, null, "*/*", Map.of());
  }

  /**
   * A {@code GET} of the whole body, accepting anything; never sent as {@code QUERY}.
   *
   * @return the request settings
   */
  public static MediaRequest get() {
    return new MediaRequest("GET", null, "*/*", Map.of());
  }

  /**
   * A {@code HEAD} request: headers only.
   *
   * @return the request settings
   */
  public static MediaRequest head() {
    return new MediaRequest("HEAD", null, "*/*", Map.of());
  }

  /**
   * An HTTP {@code QUERY} request (RFC 10008): the query dialect URL's query goes out as the
   * request content ({@link QueryMethodRequest}). The server needs {@code XI_SERVER_QUERY_METHOD}
   * (on by default); many CDNs and proxies neither cache nor forward QUERY.
   *
   * @return the request settings
   */
  public static MediaRequest query() {
    return new MediaRequest("QUERY", null, "*/*", Map.of());
  }

  /**
   * These settings with a byte range.
   *
   * @param range the range, or null for the whole body
   * @return new settings
   */
  public MediaRequest withRange(@Nullable ByteRange range) {
    return new MediaRequest(method, range, accept, headers);
  }

  /**
   * These settings with an {@code Accept} header ({@code image/avif,image/webp}, say).
   *
   * @param accept the header value
   * @return new settings
   */
  public MediaRequest withAccept(String accept) {
    return new MediaRequest(method, range, accept, headers);
  }

  /**
   * These settings with one more header.
   *
   * @param name the header name
   * @param value the value
   * @return new settings
   */
  public MediaRequest withHeader(String name, String value) {
    Map<String, String> h = new LinkedHashMap<>(headers);
    h.put(name, value);
    return new MediaRequest(method, range, accept, h);
  }
}
