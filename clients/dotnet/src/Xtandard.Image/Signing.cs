using System.Security.Cryptography;
using System.Text.RegularExpressions;

namespace Xtandard.Image;

/// <summary>
/// A signing key: <c>XI_SIGNING_KEYS</c> / <c>XI_SIGNING_SALTS</c> (imgproxy <c>IMGPROXY_KEY</c> /
/// <c>IMGPROXY_SALT</c>). <see cref="ToString"/> never shows the key material.
/// </summary>
public sealed class SigningKey
{
    private readonly byte[] key;
    private readonly byte[] salt;

    /// <summary>Creates a key from hex strings, as in the server config.</summary>
    /// <param name="key">HMAC key, hex (either case; surrounding whitespace ignored).</param>
    /// <param name="salt">Salt prepended to the message, hex.</param>
    /// <param name="size">Signature size in bytes before encoding, 1 to 32 (<c>XI_SIGNING_SIZE</c>).</param>
    /// <exception cref="UrlBuildException"><c>invalid_key</c>: not hex, or a size outside 1–32.</exception>
    public SigningKey(string key, string salt, int size = XtandardSpec.DefaultSignatureSize)
        : this(Text.KeyBytes(key, "signing key"), Text.KeyBytes(salt, "signing salt"), size)
    {
    }

    /// <summary>Creates a key from raw bytes.</summary>
    /// <param name="key">HMAC key.</param>
    /// <param name="salt">Salt prepended to the message.</param>
    /// <param name="size">Signature size in bytes before encoding, 1 to 32.</param>
    /// <exception cref="UrlBuildException"><c>invalid_key</c>: a size outside 1–32.</exception>
    public SigningKey(ReadOnlySpan<byte> key, ReadOnlySpan<byte> salt, int size = XtandardSpec.DefaultSignatureSize)
    {
        if (size is < XtandardSpec.MinSignatureSize or > XtandardSpec.MaxSignatureSize)
        {
            throw new UrlBuildException(
                UrlBuildErrorCode.InvalidKey,
                $"signature size {size} out of range [{XtandardSpec.MinSignatureSize}, {XtandardSpec.MaxSignatureSize}]");
        }

        this.key = key.ToArray();
        this.salt = salt.ToArray();
        Size = size;
    }

    /// <summary>Signature size in bytes before encoding.</summary>
    public int Size { get; }

    /// <summary>The salted HMAC-SHA256 of a message, truncated to <see cref="Size"/> and base64url-encoded.</summary>
    internal string Mac(string message)
    {
        var msg = Text.Utf8.GetBytes(message);
        var data = new byte[salt.Length + msg.Length];
        salt.CopyTo(data, 0);
        msg.CopyTo(data, salt.Length);
        Span<byte> mac = stackalloc byte[HMACSHA256.HashSizeInBytes];
        HMACSHA256.HashData(key, data, mac);
        return Text.B64Url(mac[..Size]);
    }

    /// <summary>A description without the key material.</summary>
    /// <returns><c>SigningKey(size=…)</c>.</returns>
    public override string ToString() => $"SigningKey(size={Size})";
}

/// <summary>
/// URL signing, byte-compatible with the server (internal/security/sign).
/// </summary>
/// <remarks>
/// <list type="bullet">
/// <item>imgproxy: <c>base64url(HMAC-SHA256(key, salt ‖ path)[:size])</c>, the signature segment of
/// <c>/{signature}/{options}/{source}</c>; <c>/video</c> URLs sign <c>"/video" + path</c>.</item>
/// <item>xi_sig: the same construction over <c>path ‖ "?" ‖ query-without-xi_sig</c>, appended as the
/// last query parameter.</item>
/// </list>
/// </remarks>
public static partial class Signing
{
    /// <summary>Signs a message with the salted HMAC-SHA256 of the imgproxy and xi_sig schemes.</summary>
    /// <remarks>Like the server, a message without a leading <c>/</c> is signed as if it had one.</remarks>
    /// <param name="message">The message (an imgproxy path, or an xi_sig message).</param>
    /// <param name="key">The signing key.</param>
    /// <returns>The base64url signature.</returns>
    public static string Sign(string message, SigningKey key)
    {
        ArgumentNullException.ThrowIfNull(message);
        ArgumentNullException.ThrowIfNull(key);
        return key.Mac(message.StartsWith('/') ? message : "/" + message);
    }

    /// <summary>Returns <c>"/" + signature + path</c> for an imgproxy path (everything after the signature segment).</summary>
    /// <param name="path">The path.</param>
    /// <param name="key">The signing key.</param>
    /// <returns>The signed path.</returns>
    public static string SignImgproxyPath(string path, SigningKey key)
    {
        ArgumentNullException.ThrowIfNull(path);
        var p = path.StartsWith('/') ? path : "/" + path;
        return $"/{Sign(p, key)}{p}";
    }

    /// <summary>
    /// Splits a raw query into its xi_sig value and the message xi_sig signs: the path, then <c>?</c>
    /// and the remaining non-empty parameters in their original order and encoding.
    /// </summary>
    /// <param name="path">The escaped path after the mount.</param>
    /// <param name="rawQuery">The raw query, without <c>?</c>.</param>
    /// <returns>The xi_sig value (null when absent) and the message.</returns>
    /// <exception cref="UrlBuildException"><c>invalid_request</c>: more than one xi_sig (the server answers 403).</exception>
    public static (string? Signature, string Message) XiMessage(string path, string rawQuery)
    {
        ArgumentNullException.ThrowIfNull(path);
        ArgumentNullException.ThrowIfNull(rawQuery);
        string? sig = null;
        var kept = new List<string>();
        foreach (var part in rawQuery.Split('&'))
        {
            if (part.Length == 0)
            {
                continue;
            }

            var eq = part.IndexOf('=', StringComparison.Ordinal);
            var k = eq < 0 ? part : part[..eq];
            if (k == XtandardSpec.XiSigParam)
            {
                if (sig is not null)
                {
                    throw new UrlBuildException(UrlBuildErrorCode.InvalidRequest, $"query has more than one {XtandardSpec.XiSigParam} parameter");
                }

                sig = eq < 0 ? string.Empty : part[(eq + 1)..];
                continue;
            }

            kept.Add(part);
        }

        return (sig, kept.Count > 0 ? $"{path}?{string.Join('&', kept)}" : path);
    }

    /// <summary>Returns rawQuery without any xi_sig and with a fresh xi_sig for path appended last.</summary>
    /// <param name="path">The escaped path the dialect sees, after its mount prefix.</param>
    /// <param name="rawQuery">The raw query (a leading <c>?</c> is ignored).</param>
    /// <param name="key">The signing key.</param>
    /// <returns>The signed query, without <c>?</c>.</returns>
    public static string SignXiQuery(string path, string rawQuery, SigningKey key)
    {
        ArgumentNullException.ThrowIfNull(rawQuery);
        var (_, msg) = XiMessage(path, rawQuery.StartsWith('?') ? rawQuery[1..] : rawQuery);
        var sig = Sign(msg, key);
        var q = msg[path.Length..];
        return q.Length == 0 ? $"{XtandardSpec.XiSigParam}={sig}" : $"{q[1..]}&{XtandardSpec.XiSigParam}={sig}";
    }

    /// <summary>Signs a URL (absolute, or a path with an optional query) with xi_sig. A fragment is dropped.</summary>
    /// <param name="url">The URL.</param>
    /// <param name="key">The signing key.</param>
    /// <param name="mount">The dialect's mount prefix, which the signed path excludes (<c>/q</c> for the query dialect).</param>
    /// <returns>The signed URL.</returns>
    /// <exception cref="UrlBuildException"><c>invalid_request</c>: the path is not under the mount, or the query has two xi_sig.</exception>
    public static string SignXiUrl(string url, SigningKey key, string mount = "")
    {
        ArgumentNullException.ThrowIfNull(url);
        ArgumentNullException.ThrowIfNull(mount);
        var hash = url.IndexOf('#', StringComparison.Ordinal);
        var noHash = hash < 0 ? url : url[..hash];
        var qi = noHash.IndexOf('?', StringComparison.Ordinal);
        var head = qi < 0 ? noHash : noHash[..qi];
        var query = qi < 0 ? string.Empty : noHash[(qi + 1)..];
        var pathStart = Absolute().IsMatch(head)
            ? head.IndexOf('/', head.IndexOf("//", StringComparison.Ordinal) + 2)
            : 0;
        var origin = pathStart < 0 ? head : head[..pathStart];
        var path = pathStart < 0 ? "/" : head[pathStart..];
        var m = mount.TrimEnd('/');
        if (m.Length > 0)
        {
            if (path != m && !path.StartsWith(m + "/", StringComparison.Ordinal))
            {
                throw new UrlBuildException(UrlBuildErrorCode.InvalidRequest, $"path {path} is not under the mount {m}");
            }

            path = path[m.Length..];
            if (path.Length == 0)
            {
                path = "/";
            }
        }

        return $"{origin}{m}{path}?{SignXiQuery(path, query, key)}";
    }

    [GeneratedRegex("^[a-zA-Z][a-zA-Z0-9+.-]*://")]
    private static partial Regex Absolute();
}
