using System.Buffers;
using System.Globalization;
using System.Text;

namespace Xtandard.Image;

/// <summary>Byte and text helpers shared by signing, encryption and the URL builders.</summary>
internal static class Text
{
    private const string AsciiAlphanumerics = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";

    private static readonly SearchValues<char> PlainKeep = SearchValues.Create(AsciiAlphanumerics + XtandardSpec.PlainSafe);
    private static readonly SearchValues<char> QueryKeep = SearchValues.Create(AsciiAlphanumerics + XtandardSpec.QuerySafe);
    private static readonly SearchValues<char> Alphanumerics = SearchValues.Create(AsciiAlphanumerics);

    /// <summary>UTF-8 without a BOM; lone surrogates become U+FFFD, as with TextEncoder.</summary>
    internal static readonly UTF8Encoding Utf8 = new(encoderShouldEmitUTF8Identifier: false, throwOnInvalidBytes: false);

    /// <summary>Decodes hex key material (either case, surrounding whitespace ignored).</summary>
    internal static byte[] KeyBytes(string hex, string what)
    {
        ArgumentNullException.ThrowIfNull(hex);
        var s = hex.Trim();
        if (s.Length % 2 != 0 || !IsHex(s))
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidKey, $"{what} is not a hex string");
        }

        return Convert.FromHexString(s);
    }

    private static bool IsHex(string s)
    {
        foreach (var c in s)
        {
            if (!char.IsAsciiHexDigit(c))
            {
                return false;
            }
        }

        return true;
    }

    /// <summary>Unpadded base64url (RFC 4648 §5).</summary>
    internal static string B64Url(ReadOnlySpan<byte> bytes)
    {
        var s = Convert.ToBase64String(bytes);
        var end = s.Length;
        while (end > 0 && s[end - 1] == '=')
        {
            end--;
        }

        return string.Create(end, s, static (span, src) =>
        {
            for (var i = 0; i < span.Length; i++)
            {
                span[i] = src[i] switch
                {
                    '+' => '-',
                    '/' => '_',
                    var c => c,
                };
            }
        });
    }

    /// <summary>Whether s contains an ASCII control character (C0 or DEL).</summary>
    internal static bool HasControlChar(string s)
    {
        foreach (var c in s)
        {
            if (c < 0x20 || c == 0x7f)
            {
                return true;
            }
        }

        return false;
    }

    /// <summary>Rejects null, an empty string or one with control characters.</summary>
    internal static string CheckText(string? s, UrlBuildErrorCode code, string what)
    {
        if (string.IsNullOrEmpty(s) || HasControlChar(s))
        {
            throw new UrlBuildException(code, $"{what} must be a non-empty string without control characters");
        }

        return s;
    }

    /// <summary>Rejects an extension that is not alphanumeric (<see cref="XtandardSpec.ExtensionPattern"/>).</summary>
    internal static string CheckExtension(string ext)
    {
        if (ext.Length == 0 || ext.AsSpan().ContainsAnyExcept(Alphanumerics))
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidExtension, $"invalid extension '{ext}'");
        }

        return ext;
    }

    /// <summary>Checks a string option argument: it is never escaped, so the grammar's characters are refused.</summary>
    internal static string CheckOptionArgument(string a, string separator, string where)
    {
        if (a.Contains(separator, StringComparison.Ordinal) || HasBadArgumentChar(a))
        {
            throw new UrlBuildException(
                UrlBuildErrorCode.InvalidArgument,
                $"{where}: argument '{a}' contains a character that cannot appear in an option");
        }

        return a;
    }

    private static bool HasBadArgumentChar(string a)
    {
        foreach (var c in a)
        {
            if (c is '/' or '?' or '#' or '﻿' || c < 0x20 || c == 0x7f || char.IsWhiteSpace(c))
            {
                return true;
            }
        }

        return false;
    }

    /// <summary>Percent-escapes a source for a <c>plain/</c> URL, keeping letters, digits and <see cref="XtandardSpec.PlainSafe"/>.</summary>
    internal static string EscapePlain(string s) => Escape(s, PlainKeep);

    /// <summary>Percent-escapes a query key or value, keeping letters, digits and <see cref="XtandardSpec.QuerySafe"/>.</summary>
    internal static string EscapeQuery(string s) => Escape(s, QueryKeep);

    private static string Escape(string s, SearchValues<char> keep)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (!s.AsSpan().ContainsAnyExcept(keep))
        {
            return s;
        }

        var sb = new StringBuilder(s.Length * 3);
        Span<byte> utf8 = stackalloc byte[4];
        var span = s.AsSpan();
        while (!span.IsEmpty)
        {
            if (keep.Contains(span[0]))
            {
                sb.Append(span[0]);
                span = span[1..];
                continue;
            }

            // A lone surrogate decodes to U+FFFD, as TextEncoder writes it.
            Rune.DecodeFromUtf16(span, out var rune, out var consumed);
            var n = rune.EncodeToUtf8(utf8);
            for (var i = 0; i < n; i++)
            {
                sb.Append('%').Append(utf8[i].ToString("X2", CultureInfo.InvariantCulture));
            }

            span = span[consumed..];
        }

        return sb.ToString();
    }

    /// <summary>
    /// Formats a number like ECMAScript <c>Number::toString</c> (300, 1.5, 0.1, 1e+21, 1e-7), so URLs
    /// and their signatures match across languages.
    /// </summary>
    internal static string FormatNumber(double v, string where)
    {
        if (!double.IsFinite(v))
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidArgument, $"{where}: {v} is not a finite number");
        }

        if (v == 0)
        {
            return "0";
        }

        // .NET's "R" is the shortest round-trip representation, like ECMAScript's digit choice.
        var r = Math.Abs(v).ToString("R", CultureInfo.InvariantCulture);
        var e = r.IndexOfAny(['E', 'e']);
        var mantissa = e < 0 ? r : r[..e];
        var exp = e < 0 ? 0 : int.Parse(r.AsSpan(e + 1), NumberStyles.AllowLeadingSign, CultureInfo.InvariantCulture);
        var dot = mantissa.IndexOf('.', StringComparison.Ordinal);
        var intPart = dot < 0 ? mantissa : mantissa[..dot];
        var frac = dot < 0 ? string.Empty : mantissa[(dot + 1)..];
        var all = intPart + frac;
        var lead = all.Length - all.TrimStart('0').Length;
        var digits = all[lead..].TrimEnd('0');

        // |v| = 0.<digits> × 10^n (ECMAScript's k digits and exponent n).
        var n = intPart.Length + exp - lead;
        var k = digits.Length;
        string out_;
        if (k <= n && n <= 21)
        {
            out_ = digits + new string('0', n - k);
        }
        else if (n > 0 && n <= 21)
        {
            out_ = digits[..n] + "." + digits[n..];
        }
        else if (n > -6 && n <= 0)
        {
            out_ = "0." + new string('0', -n) + digits;
        }
        else
        {
            var e1 = n - 1;
            var es = "e" + (e1 >= 0 ? "+" : "-") + Math.Abs(e1).ToString(CultureInfo.InvariantCulture);
            out_ = k == 1 ? digits + es : digits[..1] + "." + digits[1..] + es;
        }

        return v < 0 ? "-" + out_ : out_;
    }
}
