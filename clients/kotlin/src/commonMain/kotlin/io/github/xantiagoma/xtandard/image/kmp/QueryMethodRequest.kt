package io.github.xantiagoma.xtandard.image.kmp

/**
 * A query dialect request sent with the HTTP QUERY method (RFC 10008, URL-DIALECTS §4.4): the built query dialect
 * URL split at the first `?`. Make one with [queryMethodRequest].
 *
 * The server must have `XI_SERVER_QUERY_METHOD` on (the default); many CDNs and proxies neither cache nor forward
 * QUERY.
 *
 * @property method `QUERY`
 * @property url the URL without its query
 * @property contentType [CONTENT_TYPE]
 * @property body the URL's query exactly as built, `xi_sig` included: the server signs, limits and caches it as the
 *   GET URL, since `xi_sig` does not cover the method
 */
public data class QueryMethodRequest(
    val method: String,
    val url: String,
    val contentType: String,
    val body: String,
) {
    /** Constants. */
    public companion object {
        /** The content type of the request content. */
        public const val CONTENT_TYPE: String = "application/x-www-form-urlencoded"
    }
}

/**
 * Splits a built query dialect URL (`/q/…?url=…`) into an HTTP QUERY request: the parameters move from the URL to the
 * content, so long option chains, watermark text or encrypted sources stay out of URLs.
 *
 * ```
 * val q = queryMethodRequest(QueryUrlBuilder().url("s3://b/cat.jpg", queryParams { w(300) }))
 * // QueryMethodRequest(method=QUERY, url=/q/, contentType=application/x-www-form-urlencoded, body=url=s3://b/cat.jpg&w=300)
 * ```
 *
 * @param url a query dialect URL
 * @return the QUERY request
 * @throws UrlBuildException [ErrorCode.INVALID_REQUEST] for a URL without a query, with an empty query, or with a
 *   fragment
 */
public fun queryMethodRequest(url: String): QueryMethodRequest {
    val i = url.indexOf('?')
    if (i < 0 || i == url.length - 1 || '#' in url) {
        fail(ErrorCode.INVALID_REQUEST, "a QUERY request needs a query dialect URL with a query and no fragment")
    }
    return QueryMethodRequest("QUERY", url.substring(0, i), QueryMethodRequest.CONTENT_TYPE, url.substring(i + 1))
}
