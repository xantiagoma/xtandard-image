namespace Xtandard.Image;

/// <summary>Settings shared by every URL an <see cref="ImgproxyUrlBuilder"/> makes.</summary>
public sealed record ImgproxyConfig
{
    /// <summary>Prepended to every path: the server origin and the dialect's mount prefix, if any. Default <c>""</c> (root-relative paths).</summary>
    public string BaseUrl { get; init; } = string.Empty;

    /// <summary>Signs URLs with the imgproxy scheme; without it the signature segment is <see cref="UnsignedSignature"/>.</summary>
    public SigningKey? Signing { get; init; }

    /// <summary>Signature segment of unsigned URLs: <c>insecure</c> (default), <c>unsafe</c> or <c>_</c>.</summary>
    public string UnsignedSignature { get; init; } = XtandardSpec.DefaultUnsigned;

    /// <summary>Key for encrypted sources and PDF passwords.</summary>
    public EncryptionKey? Encryption { get; init; }

    /// <summary>Default source encoding.</summary>
    public SourceEncoding SourceEncoding { get; init; } = SourceEncoding.Base64;

    /// <summary>Splits base64 and encrypted sources into <c>/</c>-separated chunks of this many characters.</summary>
    public int? SplitSource { get; init; }

    /// <summary>Argument separator (<c>XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR</c>). Default <c>:</c>.</summary>
    public string Separator { get; init; } = XtandardSpec.DefaultSeparator;

    /// <summary>How option names are written. Default <see cref="OptionNameStyle.Short"/>.</summary>
    public OptionNameStyle OptionNames { get; init; } = OptionNameStyle.Short;
}

/// <summary>
/// Builds imgproxy-dialect URLs for an xtandard-image (or imgproxy) server (docs/URL-DIALECTS.md §3.1).
/// It is immutable and thread-safe.
/// </summary>
/// <remarks>
/// <code>
/// {base_url}[/info]/{signature}/{option}/…[/-/{option}/…]/{source}
/// {base_url}/video/{signature}/{video option}/…/{source}   (signs "/video" + path)
/// </code>
/// Sources are base64url (default), <c>plain/</c> (percent-escaped, <c>@ext</c>) or <c>enc/</c>
/// (AES-CBC); base64 and enc sources take <c>.ext</c>.
/// <code>
/// var img = new ImgproxyUrlBuilder(new() { BaseUrl = "https://img.example.com", Signing = new SigningKey(key, salt) });
/// img.Url("s3://bucket/cat.jpg", new() { { "rs", "fill", 300, 400 }, { "q", 80 } }, extension: "webp");
/// img.Image("s3://bucket/cat.jpg").Resize("fill", 300, 400).Quality(80).Extension("webp").Build();
/// </code>
/// </remarks>
/// <param name="config">The settings; null for the defaults (unsigned, base64, short names).</param>
public sealed class ImgproxyUrlBuilder(ImgproxyConfig? config = null)
{
    private enum Endpoint
    {
        Process,
        Info,
        Video,
    }

    /// <summary>The settings.</summary>
    public ImgproxyConfig Config { get; } = config ?? new();

    /// <summary>Builds a processing URL.</summary>
    /// <param name="source">The source URL (<c>https://</c>, <c>s3://</c>, <c>xi://</c>, <c>local:///</c>, …).</param>
    /// <param name="options">Processing options by name, alias or typed record, in URL order.</param>
    /// <param name="extension">Output format extension (<c>@webp</c> / <c>.webp</c>).</param>
    /// <param name="sourceEncoding">Overrides the configured source encoding.</param>
    /// <param name="pdfPassword">Plain-text password of a protected PDF (needs <see cref="ImgproxyConfig.Encryption"/> and <see cref="ImgproxyConfig.Signing"/>).</param>
    /// <returns>The URL.</returns>
    /// <exception cref="UrlBuildException">The options, source or keys cannot make a URL.</exception>
    public string Url(string source, ProcessingOptions? options = null, string? extension = null, SourceEncoding? sourceEncoding = null, string? pdfPassword = null) =>
        Build(Endpoint.Process, [Entries(options)], source, sourceEncoding, extension, pdfPassword);

    /// <summary>Builds a processing URL with chained pipelines (<c>/-/</c>): the image is processed by each in turn.</summary>
    /// <param name="source">The source URL.</param>
    /// <param name="pipelines">One options collection per pipeline.</param>
    /// <param name="extension">Output format extension.</param>
    /// <param name="sourceEncoding">Overrides the configured source encoding.</param>
    /// <param name="pdfPassword">Plain-text password of a protected PDF.</param>
    /// <returns>The URL.</returns>
    /// <exception cref="UrlBuildException">The options, source or keys cannot make a URL.</exception>
    public string ChainedUrl(string source, IEnumerable<ProcessingOptions> pipelines, string? extension = null, SourceEncoding? sourceEncoding = null, string? pdfPassword = null)
    {
        ArgumentNullException.ThrowIfNull(pipelines);
        return Build(Endpoint.Process, [.. pipelines.Select(Entries)], source, sourceEncoding, extension, pdfPassword);
    }

    /// <summary>Builds an <c>/info</c> URL.</summary>
    /// <param name="source">The source URL.</param>
    /// <param name="options">/info options.</param>
    /// <param name="sourceEncoding">Overrides the configured source encoding.</param>
    /// <param name="pdfPassword">Plain-text password of a protected PDF.</param>
    /// <returns>The URL.</returns>
    /// <exception cref="UrlBuildException">The options, source or keys cannot make a URL.</exception>
    public string InfoUrl(string source, InfoOptions? options = null, SourceEncoding? sourceEncoding = null, string? pdfPassword = null) =>
        Build(Endpoint.Info, [Entries(options)], source, sourceEncoding, null, pdfPassword);

    /// <summary>Builds a <c>/video</c> URL: a video transformation, audio or a waveform.</summary>
    /// <param name="source">The source URL.</param>
    /// <param name="options">/video options.</param>
    /// <param name="extension">The container (<see cref="VideoContainers"/>).</param>
    /// <param name="sourceEncoding">Overrides the configured source encoding.</param>
    /// <returns>The URL.</returns>
    /// <exception cref="UrlBuildException">The options, source or keys cannot make a URL.</exception>
    public string VideoUrl(string source, VideoOptions? options = null, string? extension = null, SourceEncoding? sourceEncoding = null) =>
        Build(Endpoint.Video, [Entries(options)], source, sourceEncoding, extension, null);

    /// <summary>Builds an HLS or DASH manifest URL (<c>/video/…@m3u8</c> or <c>@mpd</c>).</summary>
    /// <param name="source">The video source.</param>
    /// <param name="protocol"><see cref="StreamProtocol.Hls"/> or <see cref="StreamProtocol.Dash"/>.</param>
    /// <param name="renditions">Rungs by the short side of the frame (at most 8); default: the server's ladder.</param>
    /// <param name="segmentDuration">Segment length in whole seconds (1 to 30).</param>
    /// <param name="segmentFormat"><c>fmp4</c> (CMAF, default) or <c>ts</c> (MPEG-TS, HLS only).</param>
    /// <param name="options">Other /video options (trim, crop, quality, audio, …) applied to every rendition.</param>
    /// <param name="sourceEncoding">Overrides the configured source encoding.</param>
    /// <returns>The URL.</returns>
    /// <exception cref="UrlBuildException">The options, source or keys cannot make a URL.</exception>
    public string StreamUrl(
        string source,
        StreamProtocol protocol,
        IEnumerable<int>? renditions = null,
        int? segmentDuration = null,
        SegmentFormat? segmentFormat = null,
        VideoOptions? options = null,
        SourceEncoding? sourceEncoding = null)
    {
        var (entries, ext) = StreamParts([Entries(options)], protocol, renditions, segmentDuration, segmentFormat);
        return Build(Endpoint.Video, [entries], source, sourceEncoding, ext, null);
    }

    /// <summary>Starts a fluent processing URL for a source.</summary>
    /// <param name="source">The source URL.</param>
    /// <returns>The builder.</returns>
    public ImgproxyUrlChain Image(string source) => new(new(source, Runner(Endpoint.Process)));

    /// <summary>Starts a fluent <c>/info</c> URL for a source.</summary>
    /// <param name="source">The source URL.</param>
    /// <returns>The builder.</returns>
    public ImgproxyInfoUrlChain Info(string source) => new(new(source, Runner(Endpoint.Info)));

    /// <summary>Starts a fluent <c>/video</c> URL for a source.</summary>
    /// <param name="source">The source URL.</param>
    /// <returns>The builder.</returns>
    public ImgproxyVideoUrlChain Video(string source) => new(new(source, Runner(Endpoint.Video)));

    private Func<ChainState, string> Runner(Endpoint endpoint) =>
        s => Build(endpoint, s.Pipelines, s.Source, s.Encoding, s.Extension, s.PdfPassword);

    internal static List<OptionEntry> Entries(IEnumerable<OptionEntry>? options) => options is null ? [] : [.. options];

    /// <summary>Rejects a separator that is not one character or that the grammar uses.</summary>
    internal static string CheckSeparator(string separator, IReadOnlyList<string> forbidden)
    {
        if (separator is not { Length: 1 } || forbidden.Contains(separator))
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidSeparator, $"invalid argument separator '{separator}'");
        }

        return separator;
    }

    /// <summary>The pdf_password token, checking the encryption key first, then signing.</summary>
    internal static string PasswordToken(EncryptionKey? encryption, SigningKey? signing, string password)
    {
        if (encryption is null)
        {
            throw new UrlBuildException(UrlBuildErrorCode.MissingEncryption, "pdf_password needs an encryption key");
        }

        if (signing is null)
        {
            throw new UrlBuildException(UrlBuildErrorCode.MissingSigning, "pdf_password needs signing: the server refuses it on unsigned URLs");
        }

        return Encryption.EncryptPdfPassword(password, encryption);
    }

    /// <summary>The video options and manifest container of a stream request.</summary>
    internal static (List<OptionEntry> Entries, string Extension) StreamParts(
        IEnumerable<IEnumerable<OptionEntry>> pipelines,
        StreamProtocol protocol,
        IEnumerable<int>? renditions,
        int? segmentDuration,
        SegmentFormat? segmentFormat)
    {
        List<OptionEntry> entries = [.. pipelines.SelectMany(p => p)];
        foreach (var e in entries)
        {
            if (Catalogue.Video.Canonical(e.Name) is "stream" or "segment_duration" or "segment_format" or "format")
            {
                throw new UrlBuildException(
                    UrlBuildErrorCode.InvalidRequest,
                    $"stream requests set {e.Name} through protocol, renditions and the segment settings");
            }
        }

        var p = protocol.Value ?? string.Empty;
        if (!XtandardSpec.StreamManifests.TryGetValue(p, out var manifest))
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidRequest, $"unknown stream protocol '{p}'");
        }

        var rungs = renditions?.ToArray() ?? [];
        if (rungs.Length > 0)
        {
            entries.Add(new("stream", [p, .. rungs.Select(r => (OptionArg)r)]));
        }

        if (segmentDuration is { } d)
        {
            entries.Add(new("segment_duration", [d]));
        }

        if (segmentFormat is { } sf)
        {
            var f = sf.Value ?? string.Empty;
            if (!XtandardSpec.SegmentFormatProtocols.TryGetValue(f, out var protocols) || !protocols.Contains(p))
            {
                throw new UrlBuildException(UrlBuildErrorCode.InvalidRequest, $"segment format '{f}' is not available for {p}");
            }

            if (f != XtandardSpec.DefaultSegmentFormat)
            {
                entries.Add(new("segment_format", [f]));
            }
        }

        return (entries, manifest.Container);
    }

    private string Build(Endpoint endpoint, IReadOnlyList<List<OptionEntry>> pipelines, string source, SourceEncoding? encoding, string? ext, string? pdfPassword)
    {
        var cfg = Config;
        var sep = CheckSeparator(cfg.Separator, XtandardSpec.ForbiddenSeparators);
        var style = cfg.OptionNames;
        var cat = endpoint switch
        {
            Endpoint.Info => Catalogue.Info,
            Endpoint.Video => Catalogue.Video,
            _ => Catalogue.Processing,
        };
        var nonEmpty = pipelines.Where(p => p.Count > 0).ToList();
        if (endpoint != Endpoint.Process && nonEmpty.Count > 1)
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidRequest, $"/{(endpoint == Endpoint.Info ? "info" : "video")} URLs have no chained pipelines");
        }

        if (endpoint == Endpoint.Video && pdfPassword is not null)
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidRequest, "/video URLs take no pdf_password");
        }

        var segments = new List<string>();
        if (pdfPassword is not null)
        {
            var token = PasswordToken(cfg.Encryption, cfg.Signing, pdfPassword);
            segments.Add($"{(style == OptionNameStyle.Canonical ? XtandardSpec.PdfPasswordName : XtandardSpec.PdfPasswordShort)}{sep}{token}");
        }

        for (var i = 0; i < nonEmpty.Count; i++)
        {
            if (i > 0)
            {
                segments.Add(XtandardSpec.PipelineSeparator);
            }

            segments.AddRange(nonEmpty[i].Select(e => cat.FormatEntry(e, sep, style)));
        }

        segments.Add(SourceSegment(cfg, source, encoding ?? cfg.SourceEncoding, ext));
        var path = "/" + string.Join('/', segments);
        var b = cfg.BaseUrl.TrimEnd('/');
        if (endpoint == Endpoint.Video)
        {
            var sig = cfg.Signing is null ? cfg.UnsignedSignature : Signing.Sign("/video" + path, cfg.Signing);
            return $"{b}/video/{sig}{path}";
        }

        var signed = cfg.Signing is null ? $"/{cfg.UnsignedSignature}{path}" : Signing.SignImgproxyPath(path, cfg.Signing);
        return $"{b}{(endpoint == Endpoint.Info ? "/info" : string.Empty)}{signed}";
    }

    private static string Split(string token, int? n)
    {
        if (n is not > 0 || token.Length <= n)
        {
            return token;
        }

        return string.Join('/', token.Chunk(n.Value).Select(c => new string(c)));
    }

    private static string SourceSegment(ImgproxyConfig cfg, string source, SourceEncoding encoding, string? ext)
    {
        Text.CheckText(source, UrlBuildErrorCode.InvalidSource, "source");
        var e = string.IsNullOrEmpty(ext) ? null : Text.CheckExtension(ext);
        switch (encoding)
        {
            case SourceEncoding.Plain:
                return $"plain/{Text.EscapePlain(source)}" + (e is null ? string.Empty : $"@{e}");
            case SourceEncoding.Base64:
                return Split(Text.B64Url(Text.Utf8.GetBytes(source)), cfg.SplitSource) + (e is null ? string.Empty : $".{e}");
            case SourceEncoding.Encrypted:
                if (cfg.Encryption is null)
                {
                    throw new UrlBuildException(UrlBuildErrorCode.MissingEncryption, "encrypted sources need an encryption key");
                }

                var token = Encryption.EncryptSource(source, cfg.Encryption);
                return $"{XtandardSpec.EncryptedSourcePrefix}{Split(token, cfg.SplitSource)}" + (e is null ? string.Empty : $".{e}");
            default:
                throw new UrlBuildException(UrlBuildErrorCode.InvalidRequest, $"unknown source encoding {encoding}");
        }
    }
}

/// <summary>A fluent processing URL: one method per option name and alias, <see cref="Pipeline"/> for <c>/-/</c>.</summary>
public sealed class ImgproxyUrlChain : ProcessingChain<ImgproxyUrlChain>
{
    internal ImgproxyUrlChain(ChainState state)
        : base(state)
    {
    }

    /// <summary>Overrides the source encoding.</summary>
    /// <param name="encoding">The encoding.</param>
    /// <returns>This builder.</returns>
    public ImgproxyUrlChain Encoding(SourceEncoding encoding) => SetEncoding(encoding);

    /// <summary>Sets the output extension (<c>@webp</c> / <c>.webp</c>).</summary>
    /// <param name="extension">The extension.</param>
    /// <returns>This builder.</returns>
    public ImgproxyUrlChain Extension(string extension) => SetExtension(extension);

    /// <summary>Starts the next chained pipeline (<c>/-/</c>).</summary>
    /// <returns>This builder.</returns>
    public ImgproxyUrlChain Pipeline() => NextPipeline();

    /// <summary>Opens a password-protected PDF: the plain-text password is encrypted into <c>pdp</c> (needs both keys).</summary>
    /// <param name="password">The password.</param>
    /// <returns>This builder.</returns>
    public ImgproxyUrlChain WithPdfPassword(string password) => SetPdfPassword(password);
}

/// <summary>A fluent <c>/info</c> URL.</summary>
public sealed class ImgproxyInfoUrlChain : InfoChain<ImgproxyInfoUrlChain>
{
    internal ImgproxyInfoUrlChain(ChainState state)
        : base(state)
    {
    }

    /// <summary>Overrides the source encoding.</summary>
    /// <param name="encoding">The encoding.</param>
    /// <returns>This builder.</returns>
    public ImgproxyInfoUrlChain Encoding(SourceEncoding encoding) => SetEncoding(encoding);

    /// <summary>Opens a password-protected PDF: the plain-text password is encrypted into <c>pdp</c> (needs both keys).</summary>
    /// <param name="password">The password.</param>
    /// <returns>This builder.</returns>
    public ImgproxyInfoUrlChain WithPdfPassword(string password) => SetPdfPassword(password);
}

/// <summary>A fluent <c>/video</c> URL: video transformations, audio and waveforms.</summary>
public sealed class ImgproxyVideoUrlChain : VideoChain<ImgproxyVideoUrlChain>
{
    internal ImgproxyVideoUrlChain(ChainState state)
        : base(state)
    {
    }

    /// <summary>Overrides the source encoding.</summary>
    /// <param name="encoding">The encoding.</param>
    /// <returns>This builder.</returns>
    public ImgproxyVideoUrlChain Encoding(SourceEncoding encoding) => SetEncoding(encoding);

    /// <summary>Sets the container (<see cref="VideoContainers"/>).</summary>
    /// <param name="extension">The container.</param>
    /// <returns>This builder.</returns>
    public ImgproxyVideoUrlChain Extension(string extension) => SetExtension(extension);
}
