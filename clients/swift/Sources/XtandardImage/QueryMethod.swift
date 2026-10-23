/// A query dialect request sent with the HTTP QUERY method (RFC 10008, URL-DIALECTS §4.4).
///
/// The parameters move from the URL to the request content, so long option
/// chains, watermark text or encrypted sources stay out of URLs. Build one with
/// ``queryMethodRequest(_:)``. The server must have `XI_SERVER_QUERY_METHOD` on
/// (the default); many CDNs and proxies neither cache nor forward QUERY, which
/// is why ``XtandardClient`` sends it only when asked.
public struct QueryMethodRequest: Sendable, Hashable {
  /// The content type of the request content.
  public static let formContentType = "application/x-www-form-urlencoded"

  /// Always `QUERY`.
  public let method: String
  /// The URL without its query.
  public let url: String
  /// Always `application/x-www-form-urlencoded`.
  public let contentType: String
  /// The URL's query exactly as built, `xi_sig` included: the server signs,
  /// limits and caches it as the GET URL, since `xi_sig` does not cover the method.
  public let body: String
}

/// Splits a built query dialect URL (`/q/…?url=…`) at its first `?` into an
/// HTTP QUERY request.
///
/// ```swift
/// let q = try queryMethodRequest("https://img.example.com/q/?url=s3://b/a.jpg&w=300")
/// // q.url == "https://img.example.com/q/", q.body == "url=s3://b/a.jpg&w=300"
/// ```
///
/// - Throws: ``URLBuildError`` with ``URLBuildError/Code/invalidRequest`` for a
///   URL without a query, with an empty query, or with a fragment.
public func queryMethodRequest(_ url: String) throws(URLBuildError) -> QueryMethodRequest {
  guard let q = url.firstIndex(of: "?"), url.index(after: q) != url.endIndex, !url.contains("#")
  else {
    throw URLBuildError(
      .invalidRequest, "a QUERY request needs a query dialect URL with a query and no fragment")
  }
  return QueryMethodRequest(
    method: HTTPMethod.query.rawValue, url: String(url[..<q]),
    contentType: QueryMethodRequest.formContentType, body: String(url[url.index(after: q)...]))
}
