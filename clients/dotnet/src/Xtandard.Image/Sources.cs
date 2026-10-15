using System.Text.RegularExpressions;

namespace Xtandard.Image;

/// <summary>
/// Source references (docs/URL-DIALECTS.md §3.1): the string a URL carries as its source, and the
/// two percent-escapes. The reference helpers only join strings; the URL builders escape or encode the result.
/// </summary>
/// <remarks>
/// <code>
/// https://host/path        an http source
/// s3://bucket/key          gs://bucket/key   abs://container/key
/// local:///path            ftp://host/path   sftp://host/path
/// xi://&lt;source&gt;/&lt;key&gt;      a named source (XI_SOURCES)
/// </code>
/// </remarks>
public static partial class Sources
{
    /// <summary>A named source reference, <c>xi://&lt;name&gt;/&lt;key&gt;</c>.</summary>
    /// <remarks>The key may be relative or, for an http source, an absolute URL: <c>XiSource("web", "https://cdn.example.com/a.jpg")</c>.</remarks>
    /// <param name="name">The source name (<c>XI_SOURCES</c>).</param>
    /// <param name="key">The object key.</param>
    /// <returns>The source reference.</returns>
    /// <exception cref="UrlBuildException"><c>invalid_name</c>: the name is not a source name.</exception>
    public static string XiSource(string name, string key)
    {
        ArgumentNullException.ThrowIfNull(name);
        ArgumentNullException.ThrowIfNull(key);
        if (Text.HasControlChar(name) || !XiName().IsMatch(name))
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidName, $"invalid source name '{name}'");
        }

        return $"xi://{name}/{key.TrimStart('/')}";
    }

    /// <summary>
    /// A source reference for a scheme: the bucket, container, host or source name, then the key.
    /// <c>local</c> takes only a path: <c>SourceUrl("local", "", "img/cat.jpg")</c> is <c>local:///img/cat.jpg</c>.
    /// </summary>
    /// <param name="scheme">A scheme of <see cref="SourceSchemes"/>.</param>
    /// <param name="authority">The bucket, container, host or source name; empty for <c>local</c>.</param>
    /// <param name="key">The object key or path.</param>
    /// <returns>The source reference.</returns>
    /// <exception cref="UrlBuildException"><c>invalid_name</c>: the authority is missing, has <c>/</c>, <c>?</c>, <c>#</c> or whitespace, or is given for <c>local</c>.</exception>
    public static string SourceUrl(string scheme, string authority, string key)
    {
        ArgumentNullException.ThrowIfNull(scheme);
        ArgumentNullException.ThrowIfNull(authority);
        ArgumentNullException.ThrowIfNull(key);
        if (scheme == SourceSchemes.Xi)
        {
            return XiSource(authority, key);
        }

        if (scheme == SourceSchemes.Local)
        {
            if (authority.Length != 0)
            {
                throw new UrlBuildException(UrlBuildErrorCode.InvalidName, "local sources have no authority: local:///<path>");
            }

            return $"local:///{key.TrimStart('/')}";
        }

        if (authority.Length == 0 || BadAuthority().IsMatch(authority))
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidName, $"invalid {scheme} authority '{authority}'");
        }

        return $"{scheme}://{authority}/{key.TrimStart('/')}";
    }

    /// <summary>
    /// Percent-escapes a source for a <c>plain/</c> URL; the server path-unescapes it. ASCII letters,
    /// digits and <see cref="XtandardSpec.PlainSafe"/> are kept; everything else (<c>%</c>, <c>@</c>,
    /// <c>?</c>, <c>#</c>, spaces, non-ASCII) is written as <c>%XX</c> per UTF-8 byte.
    /// </summary>
    /// <param name="source">The source.</param>
    /// <returns>The escaped source.</returns>
    public static string EscapePlainSource(string source) => Text.EscapePlain(source);

    /// <summary>Percent-escapes a query key or value, keeping <c>:</c>, <c>/</c> and <c>,</c> readable.</summary>
    /// <param name="value">The key or value.</param>
    /// <returns>The escaped text.</returns>
    public static string EscapeQueryValue(string value) => Text.EscapeQuery(value);

    [GeneratedRegex(XtandardSpec.XiNamePattern)]
    private static partial Regex XiName();

    [GeneratedRegex(@"[/?#\s]")]
    private static partial Regex BadAuthority();
}

/// <summary>A byte range for the <c>Range</c> header of media requests (206 Partial Content).</summary>
public readonly record struct ByteRange
{
    private ByteRange(string headerValue) => HeaderValue = headerValue;

    /// <summary>The <c>Range</c> header value (<c>bytes=0-1023</c>, <c>bytes=-500</c>).</summary>
    public string HeaderValue { get; }

    /// <summary>Bytes <paramref name="start"/> to <paramref name="end"/> (inclusive), or to the end when end is null.</summary>
    /// <param name="start">The first byte.</param>
    /// <param name="end">The last byte, inclusive.</param>
    /// <returns>The range.</returns>
    /// <exception cref="UrlBuildException"><c>invalid_range</c>: a negative value, or an end before the start.</exception>
    public static ByteRange Of(long start, long? end = null)
    {
        NonNegative(start, "start");
        if (end is not { } e)
        {
            return new($"bytes={start}-");
        }

        NonNegative(e, "end");
        if (e < start)
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidRange, $"range end {e} is before start {start}");
        }

        return new($"bytes={start}-{e}");
    }

    /// <summary>The last <paramref name="length"/> bytes.</summary>
    /// <param name="length">How many bytes.</param>
    /// <returns>The range.</returns>
    /// <exception cref="UrlBuildException"><c>invalid_range</c>: a negative length.</exception>
    public static ByteRange Suffix(long length)
    {
        NonNegative(length, "suffix");
        return new($"bytes=-{length}");
    }

    /// <summary>A Range header value written by hand, sent as is.</summary>
    /// <param name="headerValue">The header value.</param>
    /// <returns>The range.</returns>
    public static ByteRange FromHeader(string headerValue)
    {
        ArgumentNullException.ThrowIfNull(headerValue);
        return new(headerValue);
    }

    /// <summary>The <c>Range</c> header value.</summary>
    /// <returns><see cref="HeaderValue"/>.</returns>
    public override string ToString() => HeaderValue ?? string.Empty;

    private static void NonNegative(long n, string what)
    {
        if (n < 0)
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidRange, $"range {what} {n} is not a non-negative integer");
        }
    }
}
