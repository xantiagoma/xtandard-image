package io.github.xantiagoma.xtandard.image;

import java.net.http.HttpHeaders;
import java.util.Optional;

/**
 * A successful (2xx) media response.
 *
 * @param <T> the body type: {@code byte[]} by default, or what a {@link
 *     java.net.http.HttpResponse.BodyHandler} makes (an {@link java.io.InputStream} to stream a
 *     large body, a file, ...)
 * @param status the HTTP status (200, or 206 for a range)
 * @param url the request URL
 * @param headers the response headers
 * @param body the body (empty for {@code HEAD})
 */
public record MediaResponse<T>(int status, String url, HttpHeaders headers, T body) {

  /**
   * The {@code Content-Type} header.
   *
   * @return the media type, if present
   */
  public Optional<String> contentType() {
    return headers.firstValue("content-type");
  }

  /**
   * The first value of a header.
   *
   * @param name the header name (any case)
   * @return the value, if present
   */
  public Optional<String> header(String name) {
    return headers.firstValue(name);
  }
}
