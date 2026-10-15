package io.github.xantiagoma.xtandard.image;

import java.util.LinkedHashMap;
import java.util.Map;
import org.jspecify.annotations.Nullable;

/**
 * How a media request is sent: the method ({@code GET} or {@code HEAD}), a byte range, the {@code
 * Accept} header (which drives format negotiation) and extra headers. Immutable; start from {@link
 * #get()} or {@link #head()}.
 *
 * @param method {@code GET} or {@code HEAD}
 * @param range a byte range, or null for the whole body
 * @param accept the {@code Accept} header
 * @param headers extra headers, sent after the client's own
 */
public record MediaRequest(
    String method, @Nullable ByteRange range, String accept, Map<String, String> headers) {

  /**
   * Copies the headers.
   *
   * @param method the method
   * @param range the range
   * @param accept the Accept header
   * @param headers extra headers
   */
  public MediaRequest {
    if (!method.equals("GET") && !method.equals("HEAD")) {
      throw new IllegalArgumentException("method must be GET or HEAD, not " + method);
    }
    headers = Map.copyOf(headers);
  }

  /**
   * A {@code GET} of the whole body, accepting anything.
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
