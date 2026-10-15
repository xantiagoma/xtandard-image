using System.Net;
using System.Text.Json;

namespace Xtandard.Image;

/// <summary>Settings of an <see cref="XtandardClient"/>; mutable so the options pattern can bind and configure it.</summary>
public sealed class XtandardClientOptions
{
    /// <summary>Server origin, plus the imgproxy mount prefix if any (<c>http://localhost:8080</c>).</summary>
    public string BaseUrl { get; set; } = string.Empty;

    /// <summary>Signs the URLs the client builds (imgproxy scheme, and xi_sig for <see cref="XtandardClient.QueryUrls"/>).</summary>
    public SigningKey? Signing { get; set; }

    /// <summary>Key for encrypted sources and PDF passwords.</summary>
    public EncryptionKey? Encryption { get; set; }

    /// <summary>Signature segment of unsigned URLs.</summary>
    public string UnsignedSignature { get; set; } = XtandardSpec.DefaultUnsigned;

    /// <summary>Default source encoding.</summary>
    public SourceEncoding SourceEncoding { get; set; } = SourceEncoding.Base64;

    /// <summary>Splits base64 and encrypted sources into chunks of this many characters.</summary>
    public int? SplitSource { get; set; }

    /// <summary>Argument separator.</summary>
    public string Separator { get; set; } = XtandardSpec.DefaultSeparator;

    /// <summary>How option names are written.</summary>
    public OptionNameStyle OptionNames { get; set; } = OptionNameStyle.Short;

    /// <summary>The query dialect's mount prefix, for <see cref="XtandardClient.QueryUrls"/>.</summary>
    public string QueryMount { get; set; } = XtandardSpec.QueryMount;

    /// <summary>Headers sent with every request.</summary>
    public IDictionary<string, string> Headers { get; } = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);

    /// <summary>Request timeout, when the client creates its own <see cref="HttpClient"/>. Default 60 seconds.</summary>
    public TimeSpan Timeout { get; set; } = TimeSpan.FromSeconds(60);

    /// <summary>Origin of the admin listener for <see cref="XtandardClient.PlaygroundMetaAsync"/>; default <see cref="BaseUrl"/>.</summary>
    public string? PlaygroundBaseUrl { get; set; }

    /// <summary><c>XI_PLAYGROUND_PATH</c>. Default <c>/_playground/</c>.</summary>
    public string PlaygroundPath { get; set; } = "/_playground/";

    /// <summary>The imgproxy URL settings these options describe.</summary>
    /// <returns>The settings.</returns>
    public ImgproxyConfig ToImgproxyConfig() => new()
    {
        BaseUrl = BaseUrl.TrimEnd('/'),
        Signing = Signing,
        Encryption = Encryption,
        UnsignedSignature = UnsignedSignature,
        SourceEncoding = SourceEncoding,
        SplitSource = SplitSource,
        Separator = Separator,
        OptionNames = OptionNames,
    };

    /// <summary>The query dialect settings these options describe.</summary>
    /// <returns>The settings.</returns>
    public QueryConfig ToQueryConfig() => new()
    {
        BaseUrl = BaseUrl.TrimEnd('/'),
        Mount = QueryMount,
        Signing = Signing,
        Encryption = Encryption,
        Separator = Separator,
        OptionNames = OptionNames,
    };
}

/// <summary>How a media request is sent.</summary>
public sealed record MediaRequest
{
    /// <summary>GET (default) or HEAD.</summary>
    public HttpMethod Method { get; init; } = HttpMethod.Get;

    /// <summary>A byte range (206 Partial Content).</summary>
    public ByteRange? Range { get; init; }

    /// <summary>The Accept header; drives format negotiation for images. Default <c>*/*</c>.</summary>
    public string Accept { get; init; } = "*/*";

    /// <summary>Extra request headers.</summary>
    public IReadOnlyDictionary<string, string>? Headers { get; init; }

    /// <summary>Return as soon as the headers arrive and leave the body unread: read the content stream, then dispose the response.</summary>
    public bool Stream { get; init; }
}

/// <summary>The <c>/readyz</c> answer.</summary>
/// <param name="Ready">True only for 200 <c>ready</c>.</param>
/// <param name="State"><c>ready</c>, <c>starting</c> or <c>draining</c>.</param>
public sealed record Readiness(bool Ready, string State);

/// <summary>A fetched HLS playlist or DASH MPD.</summary>
/// <param name="Url">The (signed) manifest URL; hand it to a player.</param>
/// <param name="ContentType"><c>application/vnd.apple.mpegurl</c> or <c>application/dash+xml</c>.</param>
/// <param name="Body">The manifest; its child URLs (<c>/_stream/&lt;ticket&gt;/…</c>) carry a ticket and need no signing.</param>
public sealed record StreamManifest(string Url, string ContentType, string Body);

/// <summary>
/// An async client for xtandard-image on <see cref="HttpClient"/>: the JSON and health endpoints
/// (<c>/info</c>, <c>/healthz</c>, <c>/readyz</c>, the playground's <c>api/meta</c>) and media fetches
/// (images, <c>/video</c> results with Range and HEAD, HLS and DASH manifests). URLs are built and signed
/// with <see cref="Urls"/>.
/// </summary>
/// <remarks>
/// Non-2xx responses throw an <see cref="XtandardHttpException"/> subclass chosen by status; transport
/// failures and timeouts throw <see cref="XtandardTransportException"/>. With <c>IHttpClientFactory</c>, use the
/// constructor that takes an <see cref="HttpClient"/> (or <c>AddXtandardImage()</c> from Xtandard.Image.Extensions).
/// <code>
/// using var xi = new XtandardClient(new XtandardClientOptions { BaseUrl = "http://localhost:8080", Signing = key });
/// var info = await xi.InfoAsync("s3://bucket/cat.jpg", new() { { "size", true }, { "format", true } });
/// </code>
/// </remarks>
public sealed class XtandardClient : IDisposable
{
    private readonly HttpClient http;
    private readonly bool ownsHttp;
    private readonly string baseUrl;
    private readonly KeyValuePair<string, string>[] headers;
    private readonly string playgroundMeta;

    /// <summary>Creates a client with its own <see cref="HttpClient"/>, disposed with the client.</summary>
    /// <param name="options">The settings.</param>
    public XtandardClient(XtandardClientOptions options)
        : this(new HttpClient(new SocketsHttpHandler { PooledConnectionLifetime = TimeSpan.FromMinutes(5) }) { Timeout = (options ?? throw new ArgumentNullException(nameof(options))).Timeout }, options, ownsHttp: true)
    {
    }

    /// <summary>Creates a client on an <see cref="HttpClient"/> it does not own (from <c>IHttpClientFactory</c>, a typed client, or a test handler).</summary>
    /// <param name="httpClient">The HTTP client.</param>
    /// <param name="options">The settings.</param>
    public XtandardClient(HttpClient httpClient, XtandardClientOptions options)
        : this(httpClient, options, ownsHttp: false)
    {
    }

    private XtandardClient(HttpClient httpClient, XtandardClientOptions options, bool ownsHttp)
    {
        ArgumentNullException.ThrowIfNull(httpClient);
        ArgumentNullException.ThrowIfNull(options);
        http = httpClient;
        this.ownsHttp = ownsHttp;
        baseUrl = options.BaseUrl.TrimEnd('/');
        Urls = new ImgproxyUrlBuilder(options.ToImgproxyConfig());
        QueryUrls = new QueryUrlBuilder(options.ToQueryConfig());
        headers = [.. options.Headers];
        var origin = (options.PlaygroundBaseUrl ?? baseUrl).TrimEnd('/');
        playgroundMeta = $"{origin}/{options.PlaygroundPath.Trim('/')}/api/meta";
    }

    /// <summary>The imgproxy URL builder with the client's settings.</summary>
    public ImgproxyUrlBuilder Urls { get; }

    /// <summary>The query dialect URL builder with the client's settings.</summary>
    public QueryUrlBuilder QueryUrls { get; }

    /// <summary><c>GET /healthz</c>: completes while the process can serve HTTP.</summary>
    /// <param name="cancellationToken">Cancels the request.</param>
    /// <returns>A task.</returns>
    public async Task HealthzAsync(CancellationToken cancellationToken = default)
    {
        using var res = await SendAsync(HttpMethod.Get, $"{baseUrl}/healthz", "text/plain", null, null, false, cancellationToken).ConfigureAwait(false);
        await EnsureSuccessAsync(res, cancellationToken).ConfigureAwait(false);
    }

    /// <summary><c>GET /readyz</c>: the readiness state; 503 (starting, draining) is not an error.</summary>
    /// <param name="cancellationToken">Cancels the request.</param>
    /// <returns>The readiness.</returns>
    public async Task<Readiness> ReadyzAsync(CancellationToken cancellationToken = default)
    {
        var url = $"{baseUrl}/readyz";
        using var res = await SendAsync(HttpMethod.Get, url, "text/plain", null, null, false, cancellationToken).ConfigureAwait(false);
        if (res.StatusCode is not (HttpStatusCode.OK or HttpStatusCode.ServiceUnavailable))
        {
            await EnsureSuccessAsync(res, cancellationToken).ConfigureAwait(false);
        }

        var state = (await res.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false)).Trim();
        if (state is not ("ready" or "starting" or "draining"))
        {
            throw new XtandardHttpException(res.StatusCode, url, $"unexpected readiness state '{state}'");
        }

        return new(res.StatusCode == HttpStatusCode.OK && state == "ready", state);
    }

    /// <summary><c>GET /info</c> for a source, with the URL built (and signed) from the arguments.</summary>
    /// <param name="source">The source URL.</param>
    /// <param name="options">/info options.</param>
    /// <param name="sourceEncoding">Overrides the configured source encoding.</param>
    /// <param name="pdfPassword">Plain-text password of a protected PDF.</param>
    /// <param name="cancellationToken">Cancels the request.</param>
    /// <returns>The parsed body.</returns>
    public Task<InfoResponse> InfoAsync(string source, InfoOptions? options = null, SourceEncoding? sourceEncoding = null, string? pdfPassword = null, CancellationToken cancellationToken = default) =>
        InfoAtAsync(Urls.InfoUrl(source, options, sourceEncoding, pdfPassword), cancellationToken);

    /// <summary><c>GET</c> a prebuilt <c>/info/…</c> path or URL.</summary>
    /// <param name="url">The URL, or a path on the server.</param>
    /// <param name="cancellationToken">Cancels the request.</param>
    /// <returns>The parsed body.</returns>
    public async Task<InfoResponse> InfoAtAsync(string url, CancellationToken cancellationToken = default)
    {
        var abs = Absolute(url);
        using var res = await SendAsync(HttpMethod.Get, abs, "application/json", null, null, false, cancellationToken).ConfigureAwait(false);
        await EnsureSuccessAsync(res, cancellationToken).ConfigureAwait(false);
        var body = await res.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
        await using (body.ConfigureAwait(false))
        {
            return await JsonSerializer.DeserializeAsync(body, XtandardJsonContext.Default.InfoResponse, cancellationToken).ConfigureAwait(false)
                ?? throw new XtandardHttpException(res.StatusCode, abs, "empty /info body");
        }
    }

    /// <summary><c>GET</c> (or HEAD) any URL or server path: an image or video URL, a query URL, a stream part.</summary>
    /// <param name="url">The URL, or a path on the server.</param>
    /// <param name="request">Method, Range, Accept, headers and streaming.</param>
    /// <param name="cancellationToken">Cancels the request.</param>
    /// <returns>The response, which the caller disposes.</returns>
    public async Task<HttpResponseMessage> MediaAsync(string url, MediaRequest? request = null, CancellationToken cancellationToken = default)
    {
        var r = request ?? new MediaRequest();
        var res = await SendAsync(r.Method, Absolute(url), r.Accept, r.Range, r.Headers, r.Stream, cancellationToken).ConfigureAwait(false);
        try
        {
            await EnsureSuccessAsync(res, cancellationToken).ConfigureAwait(false);
            return res;
        }
        catch
        {
            res.Dispose();
            throw;
        }
    }

    /// <summary><c>GET</c> (or HEAD) a processed image; <see cref="MediaRequest.Accept"/> drives format negotiation.</summary>
    /// <param name="source">The source URL.</param>
    /// <param name="options">Processing options.</param>
    /// <param name="extension">Output format extension.</param>
    /// <param name="sourceEncoding">Overrides the configured source encoding.</param>
    /// <param name="pdfPassword">Plain-text password of a protected PDF.</param>
    /// <param name="request">Method, Accept, headers and streaming.</param>
    /// <param name="cancellationToken">Cancels the request.</param>
    /// <returns>The response, which the caller disposes.</returns>
    public Task<HttpResponseMessage> ImageAsync(
        string source,
        ProcessingOptions? options = null,
        string? extension = null,
        SourceEncoding? sourceEncoding = null,
        string? pdfPassword = null,
        MediaRequest? request = null,
        CancellationToken cancellationToken = default) =>
        MediaAsync(Urls.Url(source, options, extension, sourceEncoding, pdfPassword), request, cancellationToken);

    /// <summary><c>GET</c> (or HEAD) a <c>/video</c> result: video, audio or a waveform; <see cref="MediaRequest.Range"/> asks for part (206).</summary>
    /// <param name="source">The source URL.</param>
    /// <param name="options">/video options.</param>
    /// <param name="extension">The container.</param>
    /// <param name="sourceEncoding">Overrides the configured source encoding.</param>
    /// <param name="request">Method, Range, Accept, headers and streaming.</param>
    /// <param name="cancellationToken">Cancels the request.</param>
    /// <returns>The response, which the caller disposes.</returns>
    public Task<HttpResponseMessage> VideoAsync(
        string source,
        VideoOptions? options = null,
        string? extension = null,
        SourceEncoding? sourceEncoding = null,
        MediaRequest? request = null,
        CancellationToken cancellationToken = default) =>
        MediaAsync(Urls.VideoUrl(source, options, extension, sourceEncoding), request, cancellationToken);

    /// <summary><c>GET</c> an HLS or DASH manifest (<c>/video/…@m3u8</c> or <c>@mpd</c>).</summary>
    /// <param name="source">The video source.</param>
    /// <param name="protocol">HLS or DASH.</param>
    /// <param name="renditions">Rungs by the short side of the frame.</param>
    /// <param name="segmentDuration">Segment length in whole seconds.</param>
    /// <param name="segmentFormat"><c>fmp4</c> or <c>ts</c>.</param>
    /// <param name="options">Other /video options.</param>
    /// <param name="sourceEncoding">Overrides the configured source encoding.</param>
    /// <param name="headers">Extra request headers.</param>
    /// <param name="cancellationToken">Cancels the request.</param>
    /// <returns>The manifest.</returns>
    public async Task<StreamManifest> StreamAsync(
        string source,
        StreamProtocol protocol,
        IEnumerable<int>? renditions = null,
        int? segmentDuration = null,
        SegmentFormat? segmentFormat = null,
        VideoOptions? options = null,
        SourceEncoding? sourceEncoding = null,
        IReadOnlyDictionary<string, string>? headers = null,
        CancellationToken cancellationToken = default)
    {
        var url = Urls.StreamUrl(source, protocol, renditions, segmentDuration, segmentFormat, options, sourceEncoding);
        var ctype = XtandardSpec.StreamManifests.TryGetValue(protocol.Value, out var m) ? m.ContentType : "application/octet-stream";
        using var res = await SendAsync(HttpMethod.Get, url, $"{ctype}, */*;q=0.5", null, headers, false, cancellationToken).ConfigureAwait(false);
        await EnsureSuccessAsync(res, cancellationToken).ConfigureAwait(false);
        var body = await res.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
        return new(url, res.Content.Headers.ContentType?.ToString() ?? ctype, body);
    }

    /// <summary><c>GET</c> the playground's <c>api/meta</c> (needs <c>XI_PLAYGROUND_ENABLED</c>; admin listener by default).</summary>
    /// <param name="cancellationToken">Cancels the request.</param>
    /// <returns>The JSON document's root.</returns>
    public async Task<JsonElement> PlaygroundMetaAsync(CancellationToken cancellationToken = default)
    {
        using var res = await SendAsync(HttpMethod.Get, playgroundMeta, "application/json", null, null, false, cancellationToken).ConfigureAwait(false);
        await EnsureSuccessAsync(res, cancellationToken).ConfigureAwait(false);
        var body = await res.Content.ReadAsStreamAsync(cancellationToken).ConfigureAwait(false);
        await using (body.ConfigureAwait(false))
        {
            using var doc = await JsonDocument.ParseAsync(body, default, cancellationToken).ConfigureAwait(false);
            return doc.RootElement.Clone();
        }
    }

    /// <summary>The Range header value of a byte range (<c>bytes=0-1023</c>).</summary>
    /// <param name="range">The range.</param>
    /// <returns>The header value.</returns>
    public static string RangeHeader(ByteRange range) => range.HeaderValue;

    /// <summary>Disposes the HTTP client if this client created it.</summary>
    public void Dispose()
    {
        if (ownsHttp)
        {
            http.Dispose();
        }
    }

    private string Absolute(string url)
    {
        ArgumentNullException.ThrowIfNull(url);
        if (Uri.TryCreate(url, UriKind.Absolute, out var u) && (u.Scheme == Uri.UriSchemeHttp || u.Scheme == Uri.UriSchemeHttps))
        {
            return url;
        }

        return $"{baseUrl}{(url.StartsWith('/') ? string.Empty : "/")}{url}";
    }

    private async Task<HttpResponseMessage> SendAsync(
        HttpMethod method,
        string url,
        string accept,
        ByteRange? range,
        IReadOnlyDictionary<string, string>? extra,
        bool stream,
        CancellationToken cancellationToken)
    {
        // A URL built here is already escaped; keep it byte for byte (the signature covers it).
        var uri = new Uri(url, new UriCreationOptions { DangerousDisablePathAndQueryCanonicalization = true });
        using var req = new HttpRequestMessage(method, uri);
        foreach (var (k, v) in headers)
        {
            req.Headers.TryAddWithoutValidation(k, v);
        }

        if (extra is not null)
        {
            foreach (var (k, v) in extra)
            {
                req.Headers.Remove(k);
                req.Headers.TryAddWithoutValidation(k, v);
            }
        }

        req.Headers.Accept.Clear();
        req.Headers.TryAddWithoutValidation("Accept", accept);
        if (range is { } r)
        {
            req.Headers.TryAddWithoutValidation("Range", r.HeaderValue);
        }

        try
        {
            return await http.SendAsync(req, stream ? HttpCompletionOption.ResponseHeadersRead : HttpCompletionOption.ResponseContentRead, cancellationToken)
                .ConfigureAwait(false);
        }
        catch (HttpRequestException e)
        {
            throw new XtandardTransportException($"{method} {url}: {e.Message}", e);
        }
        catch (TaskCanceledException e) when (!cancellationToken.IsCancellationRequested)
        {
            throw new XtandardTransportException($"{method} {url}: timed out", e);
        }
    }

    private static async Task EnsureSuccessAsync(HttpResponseMessage res, CancellationToken cancellationToken)
    {
        if (res.IsSuccessStatusCode)
        {
            return;
        }

        var url = res.RequestMessage?.RequestUri?.OriginalString ?? string.Empty;
        string text;
        try
        {
            text = await res.Content.ReadAsStringAsync(cancellationToken).ConfigureAwait(false);
        }
        catch (Exception e) when (e is HttpRequestException or IOException or InvalidOperationException)
        {
            text = string.Empty;
        }

        var body = Envelope(text);
        var message = body?.Error.Message;
        if (message is null)
        {
            message = text.Trim();
            if (message.Length == 0)
            {
                message = res.ReasonPhrase ?? $"HTTP {(int)res.StatusCode}";
            }
        }

        throw XtandardHttpException.For(res.StatusCode, url, message, body);
    }

    /// <summary>The server's JSON error envelope, or null when the body is something else.</summary>
    internal static ErrorResponse? Envelope(string text)
    {
        if (string.IsNullOrWhiteSpace(text))
        {
            return null;
        }

        try
        {
            using var doc = JsonDocument.Parse(text);
            if (doc.RootElement.ValueKind != JsonValueKind.Object
                || !doc.RootElement.TryGetProperty("error", out var err)
                || err.ValueKind != JsonValueKind.Object
                || !err.TryGetProperty("kind", out var kind) || kind.ValueKind != JsonValueKind.String
                || !err.TryGetProperty("message", out var msg) || msg.ValueKind != JsonValueKind.String)
            {
                return null;
            }

            return new ErrorResponse
            {
                Error = new ErrorBody
                {
                    Kind = kind.GetString()!,
                    Message = msg.GetString()!,
                    RequestId = err.TryGetProperty("request_id", out var id) && id.ValueKind == JsonValueKind.String ? id.GetString() : null,
                },
            };
        }
        catch (JsonException)
        {
            return null;
        }
    }
}
