namespace Xtandard.Image;

/// <summary>
/// A query dialect request sent with the HTTP QUERY method (RFC 10008, URL-DIALECTS §4.4): the
/// parameters move from the URL to the request content, so long option chains, watermark text or
/// encrypted sources stay out of URLs.
/// </summary>
/// <remarks>
/// The server must have <c>XI_SERVER_QUERY_METHOD</c> on (the default); many CDNs and proxies neither
/// cache nor forward QUERY, which is why <see cref="XtandardClientOptions.QueryMethod"/> is off by default.
/// <code>
/// var q = QueryMethodRequest.FromUrl(xi.QueryUrls.Url("s3://photos/cat.jpg", new() { { "w", 300 } }));
/// // q.Url: https://img.example.com/q/, q.Body: url=s3://photos/cat.jpg&amp;w=300&amp;xi_sig=…
/// </code>
/// </remarks>
public sealed record QueryMethodRequest
{
    /// <summary>The method name, <c>QUERY</c>.</summary>
    public const string MethodName = "QUERY";

    /// <summary>The content type of the request content, <c>application/x-www-form-urlencoded</c>.</summary>
    public const string FormContentType = "application/x-www-form-urlencoded";

    private QueryMethodRequest(string url, string body)
    {
        Url = url;
        Body = body;
    }

    /// <summary>The QUERY method, for <see cref="MediaRequest.Method"/>.</summary>
    public static HttpMethod HttpMethod { get; } = new(MethodName);

    /// <summary>Always <c>QUERY</c>.</summary>
    public string Method { get; } = MethodName;

    /// <summary>The URL without its query.</summary>
    public string Url { get; }

    /// <summary>Always <c>application/x-www-form-urlencoded</c>.</summary>
    public string ContentType { get; } = FormContentType;

    /// <summary>
    /// The URL's query exactly as built, <c>xi_sig</c> included: the server signs, limits and caches it
    /// as the GET URL, since <c>xi_sig</c> does not cover the method.
    /// </summary>
    public string Body { get; }

    /// <summary>Splits a built query dialect URL (<c>/q/…?url=…</c>) at its first <c>?</c> into a QUERY request.</summary>
    /// <param name="url">The query dialect URL, as a <see cref="QueryUrlBuilder"/> builds it.</param>
    /// <returns>The request.</returns>
    /// <exception cref="UrlBuildException"><c>invalid_request</c>: the URL has no query, an empty one, or a fragment.</exception>
    public static QueryMethodRequest FromUrl(string url)
    {
        ArgumentNullException.ThrowIfNull(url);
        var i = url.IndexOf('?', StringComparison.Ordinal);
        if (i < 0 || i == url.Length - 1 || url.Contains('#', StringComparison.Ordinal))
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidRequest, "a QUERY request needs a query dialect URL with a query and no fragment");
        }

        return new(url[..i], url[(i + 1)..]);
    }
}
