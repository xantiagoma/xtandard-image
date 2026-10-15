using System.Collections;
using System.Text;

namespace Xtandard.Image;

/// <summary>Settings of query dialect URLs.</summary>
public sealed record QueryConfig
{
    /// <summary>Server origin (and any path before the mount). Default <c>""</c>.</summary>
    public string BaseUrl { get; init; } = string.Empty;

    /// <summary>The query dialect's mount prefix (<c>XI_DIALECTS_QUERY_PREFIX</c>). Default <c>/q</c>.</summary>
    public string Mount { get; init; } = XtandardSpec.QueryMount;

    /// <summary>Signs with xi_sig (the imgproxy key and salt); without it URLs are unsigned.</summary>
    public SigningKey? Signing { get; init; }

    /// <summary>Key for PDF passwords (<c>XI_ENCRYPTION_KEYS</c>).</summary>
    public EncryptionKey? Encryption { get; init; }

    /// <summary>Argument separator inside <c>o=</c>.</summary>
    public string Separator { get; init; } = XtandardSpec.DefaultSeparator;

    /// <summary>How option names are written. Default <see cref="OptionNameStyle.Short"/>.</summary>
    public OptionNameStyle OptionNames { get; init; } = OptionNameStyle.Short;
}

/// <summary>
/// Native query keys (<c>w</c>, <c>fit</c>, <c>g</c>, <c>f</c>, … or imaginary parameters) in order; keys may
/// repeat and a null value is skipped. Booleans are written <c>true</c>/<c>false</c>.
/// <code>new QueryParams { { QueryKeys.W, 300 }, { QueryKeys.F, "webp" } }</code>
/// </summary>
public sealed class QueryParams : IReadOnlyList<KeyValuePair<string, OptionArg>>
{
    private readonly List<KeyValuePair<string, OptionArg>> items = [];

    /// <summary>The number of keys.</summary>
    public int Count => items.Count;

    /// <summary>The key at an index.</summary>
    /// <param name="index">The index.</param>
    public KeyValuePair<string, OptionArg> this[int index] => items[index];

    /// <summary>Appends a key; a null value is skipped when the URL is written.</summary>
    /// <param name="key">The key.</param>
    /// <param name="value">The value.</param>
    public void Add(string key, OptionArg value)
    {
        ArgumentNullException.ThrowIfNull(key);
        items.Add(new(key, value));
    }

    /// <inheritdoc/>
    public IEnumerator<KeyValuePair<string, OptionArg>> GetEnumerator() => items.GetEnumerator();

    IEnumerator IEnumerable.GetEnumerator() => GetEnumerator();
}

/// <summary>
/// Builds query dialect URLs (docs/URL-DIALECTS.md §4; the server needs <c>XI_DIALECTS_QUERY_ENABLED=true</c>).
/// With signing, every URL ends in an <c>xi_sig</c> over the path after the mount and the query. Immutable and thread-safe.
/// </summary>
/// <remarks>
/// <code>
/// {base_url}{mount}/?url=&lt;source&gt;&amp;&lt;native key&gt;=…&amp;o=&lt;imgproxy option path&gt;[&amp;xi_sig=…]
/// {base_url}{mount}/video?url=&lt;source&gt;&amp;&lt;video option&gt;=&lt;arg,arg&gt;…[&amp;xi_sig=…]
/// {base_url}{mount}/&lt;imaginary endpoint&gt;?url=&lt;source&gt;&amp;&lt;param&gt;=…[&amp;xi_sig=…]
/// </code>
/// </remarks>
/// <param name="config">The settings; null for the defaults (mount <c>/q</c>, unsigned).</param>
public sealed class QueryUrlBuilder(QueryConfig? config = null)
{
    /// <summary>The settings.</summary>
    public QueryConfig Config { get; } = config ?? new();

    /// <summary>Builds a native <c>/q/?url=…</c> URL: native keys, then imgproxy options as <c>o=</c>.</summary>
    /// <param name="source">The source URL.</param>
    /// <param name="parameters">Native keys, written before <c>o=</c>.</param>
    /// <param name="options">Processing options, written as <c>o=</c>.</param>
    /// <param name="extension">Output format (<c>format=</c>).</param>
    /// <param name="pdfPassword">Plain-text password of a protected PDF (<c>pdp=</c>; needs both keys).</param>
    /// <returns>The URL.</returns>
    /// <exception cref="UrlBuildException">The options, source or keys cannot make a URL.</exception>
    public string Url(string source, QueryParams? parameters = null, ProcessingOptions? options = null, string? extension = null, string? pdfPassword = null) =>
        BuildImage([ImgproxyUrlBuilder.Entries(options)], source, parameters, extension, pdfPassword);

    /// <summary>Builds a native <c>/q/?url=…</c> URL with chained pipelines in <c>o=</c>.</summary>
    /// <param name="source">The source URL.</param>
    /// <param name="parameters">Native keys, written before <c>o=</c>.</param>
    /// <param name="pipelines">One options collection per pipeline.</param>
    /// <param name="extension">Output format (<c>format=</c>).</param>
    /// <param name="pdfPassword">Plain-text password of a protected PDF.</param>
    /// <returns>The URL.</returns>
    /// <exception cref="UrlBuildException">The options, source or keys cannot make a URL.</exception>
    public string ChainedUrl(string source, QueryParams? parameters, IEnumerable<ProcessingOptions> pipelines, string? extension = null, string? pdfPassword = null)
    {
        ArgumentNullException.ThrowIfNull(pipelines);
        return BuildImage([.. pipelines.Select(ImgproxyUrlBuilder.Entries)], source, parameters, extension, pdfPassword);
    }

    /// <summary>Builds a <c>/q/video</c> URL; <c>crop</c> and <c>flip</c> are written <c>c</c> and <c>fl</c>.</summary>
    /// <param name="source">The source URL.</param>
    /// <param name="options">/video options, each a key with its arguments joined by <c>,</c>, in order (a key may repeat; later wins argument by argument).</param>
    /// <param name="extension">The container (<c>format=</c>).</param>
    /// <returns>The URL.</returns>
    /// <exception cref="UrlBuildException">The options or source cannot make a URL.</exception>
    public string VideoUrl(string source, VideoOptions? options = null, string? extension = null) =>
        BuildVideo(ImgproxyUrlBuilder.Entries(options), source, extension);

    /// <summary>Builds a <c>/q/video</c> HLS or DASH manifest URL (<c>format=m3u8|mpd</c>).</summary>
    /// <param name="source">The video source.</param>
    /// <param name="protocol">HLS or DASH.</param>
    /// <param name="renditions">Rungs by the short side of the frame (at most 8).</param>
    /// <param name="segmentDuration">Segment length in whole seconds (1 to 30).</param>
    /// <param name="segmentFormat"><c>fmp4</c> (default) or <c>ts</c> (HLS only).</param>
    /// <param name="options">Other /video options applied to every rendition.</param>
    /// <returns>The URL.</returns>
    /// <exception cref="UrlBuildException">The options or source cannot make a URL.</exception>
    public string StreamUrl(
        string source,
        StreamProtocol protocol,
        IEnumerable<int>? renditions = null,
        int? segmentDuration = null,
        SegmentFormat? segmentFormat = null,
        VideoOptions? options = null)
    {
        var (entries, ext) = ImgproxyUrlBuilder.StreamParts([ImgproxyUrlBuilder.Entries(options)], protocol, renditions, segmentDuration, segmentFormat);
        return BuildVideo(entries, source, ext);
    }

    /// <summary>Builds an imaginary endpoint URL (<c>/q/resize?url=…&amp;width=300</c>).</summary>
    /// <param name="endpoint">An endpoint of <see cref="ImaginaryEndpoints"/>.</param>
    /// <param name="source">The source URL.</param>
    /// <param name="parameters">imaginary parameters.</param>
    /// <returns>The URL.</returns>
    /// <exception cref="UrlBuildException"><c>invalid_endpoint</c>: not lower-case letters, or <c>video</c>; or a bad source.</exception>
    public string ImaginaryUrl(string endpoint, string source, QueryParams? parameters = null)
    {
        ArgumentNullException.ThrowIfNull(endpoint);
        if (endpoint.Length == 0 || endpoint == "video" || endpoint.Any(c => c is < 'a' or > 'z'))
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidEndpoint, $"invalid imaginary endpoint '{endpoint}'");
        }

        List<KeyValuePair<string, string>> pairs = [new("url", Text.CheckText(source, UrlBuildErrorCode.InvalidSource, "source")), .. ParamPairs(parameters)];
        return Finish($"/{endpoint}", pairs);
    }

    /// <summary>Starts a fluent <c>/q/?url=…</c> URL for a source.</summary>
    /// <param name="source">The source URL.</param>
    /// <returns>The builder.</returns>
    public QueryUrlChain Image(string source) =>
        new(new(source, s => BuildImage(s.Pipelines, s.Source, s.Params, s.Extension, s.PdfPassword)));

    /// <summary>Starts a fluent <c>/q/video</c> URL for a source.</summary>
    /// <param name="source">The source URL.</param>
    /// <returns>The builder.</returns>
    public QueryVideoUrlChain Video(string source) =>
        new(new(source, s => BuildVideo([.. s.Pipelines.SelectMany(p => p)], s.Source, s.Extension)));

    private static IEnumerable<KeyValuePair<string, string>> ParamPairs(IEnumerable<KeyValuePair<string, OptionArg>>? parameters)
    {
        if (parameters is null)
        {
            yield break;
        }

        foreach (var (k, v) in parameters)
        {
            if (!v.IsNull)
            {
                yield return new(k, v.FormatQuery($"query key {k}"));
            }
        }
    }

    private string BuildImage(IReadOnlyList<List<OptionEntry>> pipelines, string source, IEnumerable<KeyValuePair<string, OptionArg>>? parameters, string? ext, string? pdfPassword)
    {
        var cfg = Config;
        var sep = ImgproxyUrlBuilder.CheckSeparator(cfg.Separator, XtandardSpec.ForbiddenQuerySeparators);
        var style = cfg.OptionNames;
        List<KeyValuePair<string, string>> pairs = [new("url", Text.CheckText(source, UrlBuildErrorCode.InvalidSource, "source")), .. ParamPairs(parameters)];
        var nonEmpty = pipelines.Where(p => p.Count > 0).ToList();
        if (nonEmpty.Count > 0)
        {
            var o = string.Join("/-/", nonEmpty.Select(p => string.Join('/', p.Select(e => Catalogue.Processing.FormatEntry(e, sep, style)))));
            pairs.Add(new("o", o));
        }

        if (!string.IsNullOrEmpty(ext))
        {
            pairs.Add(new("format", Text.CheckExtension(ext)));
        }

        if (pdfPassword is not null)
        {
            var token = ImgproxyUrlBuilder.PasswordToken(cfg.Encryption, cfg.Signing, pdfPassword);
            pairs.Add(new(style == OptionNameStyle.Canonical ? XtandardSpec.PdfPasswordName : XtandardSpec.PdfPasswordShort, token));
        }

        return Finish("/", pairs);
    }

    private string BuildVideo(IEnumerable<OptionEntry> entries, string source, string? ext)
    {
        List<KeyValuePair<string, string>> pairs = [new("url", Text.CheckText(source, UrlBuildErrorCode.InvalidSource, "source"))];
        foreach (var e in entries)
        {
            var key = Catalogue.Video.WrittenName(e.Name, Config.OptionNames);
            if (e.Arguments.Count == 0)
            {
                throw new UrlBuildException(UrlBuildErrorCode.InvalidArgument, $"option {e.Name}: at least one argument is required");
            }

            key = XtandardSpec.NativeVideoKeys.GetValueOrDefault(key, key);
            var values = new string[e.Arguments.Count];
            for (var i = 0; i < values.Length; i++)
            {
                var s = e.Arguments[i].FormatOption(",", $"option {e.Name} argument {i + 1}");
                if (s.Contains(':', StringComparison.Ordinal))
                {
                    throw new UrlBuildException(UrlBuildErrorCode.InvalidArgument, $"option {e.Name} argument {i + 1}: '{s}' contains ':'");
                }

                values[i] = s;
            }

            pairs.Add(new(key, string.Join(',', values)));
        }

        if (!string.IsNullOrEmpty(ext))
        {
            pairs.Add(new("format", Text.CheckExtension(ext)));
        }

        return Finish("/video", pairs);
    }

    private string Finish(string path, List<KeyValuePair<string, string>> pairs)
    {
        var cfg = Config;
        var query = new StringBuilder();
        foreach (var (k, v) in pairs)
        {
            if (query.Length > 0)
            {
                query.Append('&');
            }

            query.Append(Text.EscapeQuery(k)).Append('=').Append(Text.EscapeQuery(v));
        }

        var q = cfg.Signing is null ? query.ToString() : Signing.SignXiQuery(path, query.ToString(), cfg.Signing);
        return $"{cfg.BaseUrl.TrimEnd('/')}{cfg.Mount.TrimEnd('/')}{path}?{q}";
    }
}

/// <summary>A fluent <c>/q/?url=…</c> URL: the option methods write <c>o=</c>, <see cref="Param"/> a native key.</summary>
public sealed class QueryUrlChain : ProcessingChain<QueryUrlChain>
{
    internal QueryUrlChain(ChainState state)
        : base(state)
    {
    }

    /// <summary>Appends a native key (<c>w</c>, <c>fit</c>, <c>g</c>, …), written before <c>o=</c>; a null value is skipped.</summary>
    /// <param name="key">The key.</param>
    /// <param name="value">The value.</param>
    /// <returns>This builder.</returns>
    public QueryUrlChain Param(string key, OptionArg value)
    {
        ArgumentNullException.ThrowIfNull(key);
        State.Params.Add(new(key, value));
        return this;
    }

    /// <summary>Sets the output format (<c>format=</c>).</summary>
    /// <param name="extension">The extension.</param>
    /// <returns>This builder.</returns>
    public QueryUrlChain Extension(string extension) => SetExtension(extension);

    /// <summary>Starts the next chained pipeline inside <c>o=</c> (<c>/-/</c>).</summary>
    /// <returns>This builder.</returns>
    public QueryUrlChain Pipeline() => NextPipeline();

    /// <summary>Opens a password-protected PDF (<c>pdp=</c>; needs both keys).</summary>
    /// <param name="password">The password.</param>
    /// <returns>This builder.</returns>
    public QueryUrlChain WithPdfPassword(string password) => SetPdfPassword(password);
}

/// <summary>A fluent <c>/q/video</c> URL: one method per video option name and alias.</summary>
public sealed class QueryVideoUrlChain : VideoChain<QueryVideoUrlChain>
{
    internal QueryVideoUrlChain(ChainState state)
        : base(state)
    {
    }

    /// <summary>Sets the container (<c>format=</c>).</summary>
    /// <param name="extension">The container.</param>
    /// <returns>This builder.</returns>
    public QueryVideoUrlChain Extension(string extension) => SetExtension(extension);
}
