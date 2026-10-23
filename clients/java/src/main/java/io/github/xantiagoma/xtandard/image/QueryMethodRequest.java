package io.github.xantiagoma.xtandard.image;

/**
 * A query dialect request sent with the HTTP QUERY method (RFC 10008, URL-DIALECTS §4.4): the built
 * query dialect URL split at the first {@code ?}. Start from {@link #from(String)}.
 *
 * <p>The server must have {@code XI_SERVER_QUERY_METHOD} on (the default); many CDNs and proxies
 * neither cache nor forward QUERY.
 *
 * @param method {@code QUERY}
 * @param url the URL without its query
 * @param contentType {@link #CONTENT_TYPE}
 * @param body the URL's query exactly as built, {@code xi_sig} included: the server signs, limits
 *     and caches it as the GET URL, since {@code xi_sig} does not cover the method
 */
public record QueryMethodRequest(String method, String url, String contentType, String body) {
  /** The content type of the request content. */
  public static final String CONTENT_TYPE = "application/x-www-form-urlencoded";

  /**
   * Splits a built query dialect URL ({@code /q/...?url=...}) into an HTTP QUERY request: the
   * parameters move from the URL to the content, so long option chains, watermark text or encrypted
   * sources stay out of URLs.
   *
   * <pre>{@code
   * QueryMethodRequest q = QueryMethodRequest.from(queryUrls.url(source, options, params));
   * // q.method() "QUERY", q.url() ".../q/", q.contentType(), q.body() "url=...&w=300&xi_sig=..."
   * }</pre>
   *
   * @param url a query dialect URL
   * @return the QUERY request
   * @throws UrlBuildException {@link ErrorCode#INVALID_REQUEST} for a URL without a query, with an
   *     empty query, or with a fragment
   */
  public static QueryMethodRequest from(String url) {
    int i = url.indexOf('?');
    if (i < 0 || i == url.length() - 1 || url.indexOf('#') >= 0) {
      throw new UrlBuildException(
          ErrorCode.INVALID_REQUEST,
          "a QUERY request needs a query dialect URL with a query and no fragment");
    }
    return new QueryMethodRequest("QUERY", url.substring(0, i), CONTENT_TYPE, url.substring(i + 1));
  }
}
