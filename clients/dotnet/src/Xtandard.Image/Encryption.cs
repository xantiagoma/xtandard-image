using System.Security.Cryptography;

namespace Xtandard.Image;

/// <summary>
/// An encryption key: the first of <c>XI_ENCRYPTION_KEYS</c> (imgproxy
/// <c>IMGPROXY_SOURCE_URL_ENCRYPTION_KEY</c>). <see cref="ToString"/> never shows the key material.
/// </summary>
/// <remarks>
/// Leave the IV out for a fresh random IV per token, as the server's own encoder does. A fixed IV
/// makes URLs deterministic and reveals when two URLs share a source; use it only for tests or cache keys.
/// </remarks>
public sealed class EncryptionKey
{
    private readonly byte[] key;
    private readonly byte[]? iv;

    /// <summary>Creates a key from hex strings, as in the server config.</summary>
    /// <param name="key">AES key, hex: 16, 24 or 32 bytes.</param>
    /// <param name="iv">A fixed 16-byte IV, hex; null for a random IV per token.</param>
    /// <exception cref="UrlBuildException"><c>invalid_key</c>: not hex, a wrong key size, or an IV that is not 16 bytes.</exception>
    public EncryptionKey(string key, string? iv = null)
        : this(Text.KeyBytes(key, "encryption key"), iv is null ? null : Text.KeyBytes(iv, "iv"))
    {
    }

    /// <summary>Creates a key from raw bytes.</summary>
    /// <param name="key">AES key: 16, 24 or 32 bytes.</param>
    /// <param name="iv">A fixed 16-byte IV; null for a random IV per token.</param>
    /// <exception cref="UrlBuildException"><c>invalid_key</c>: a wrong key size, or an IV that is not 16 bytes.</exception>
    public EncryptionKey(byte[] key, byte[]? iv = null)
    {
        ArgumentNullException.ThrowIfNull(key);
        if (!XtandardSpec.EncryptionKeySizes.Contains(key.Length))
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidKey, $"encryption key is {key.Length} bytes, want 16, 24 or 32");
        }

        if (iv is not null && iv.Length != XtandardSpec.IvSize)
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidKey, $"iv is not {XtandardSpec.IvSize} bytes");
        }

        this.key = [.. key];
        this.iv = iv is null ? null : [.. iv];
    }

    /// <summary>Whether tokens use a fixed IV.</summary>
    public bool HasFixedIv => iv is not null;

    /// <summary><c>base64url(IV ‖ AES-CBC(key, IV, PKCS#7(utf8(plaintext))))</c>.</summary>
    internal string Seal(string plaintext)
    {
        var nonce = iv ?? RandomNumberGenerator.GetBytes(XtandardSpec.IvSize);
        using var aes = Aes.Create();
        aes.Key = key;
        var data = Text.Utf8.GetBytes(plaintext);
        var sealedLength = aes.GetCiphertextLengthCbc(data.Length, PaddingMode.PKCS7);
        var token = new byte[XtandardSpec.IvSize + sealedLength];
        nonce.CopyTo(token, 0);
        aes.EncryptCbc(data, nonce, token.AsSpan(XtandardSpec.IvSize), PaddingMode.PKCS7);
        return Text.B64Url(token);
    }

    /// <summary>A description without the key material.</summary>
    /// <returns><c>EncryptionKey(…)</c>.</returns>
    public override string ToString() => $"EncryptionKey(bits={key.Length * 8}, fixed_iv={HasFixedIv})";
}

/// <summary>
/// Source URL and PDF password encryption, byte-compatible with the server
/// (internal/security/crypt): <c>token = base64url_unpadded(IV[16] ‖ AES-CBC(key, IV, PKCS#7(utf8(plaintext))))</c>.
/// Encrypt on a server: the key decrypts every token.
/// </summary>
public static class Encryption
{
    /// <summary>Encrypts a source URL into an <c>enc/</c> token.</summary>
    /// <param name="source">The source.</param>
    /// <param name="key">The encryption key.</param>
    /// <returns>The token.</returns>
    /// <exception cref="UrlBuildException"><c>invalid_source</c>: the source is empty or has control characters.</exception>
    public static string EncryptSource(string source, EncryptionKey key)
    {
        ArgumentNullException.ThrowIfNull(key);
        return key.Seal(Text.CheckText(source, UrlBuildErrorCode.InvalidSource, "source"));
    }

    /// <summary>
    /// Encrypts the password of a password-protected PDF into a <c>pdf_password</c> (<c>pdp</c>) token:
    /// the <c>enc/</c> format over the password, with the same key. The server accepts it on signed URLs only.
    /// </summary>
    /// <param name="password">The plain-text password.</param>
    /// <param name="key">The encryption key.</param>
    /// <returns>The token.</returns>
    /// <exception cref="UrlBuildException"><c>invalid_password</c>: empty, with control characters, or over 1024 UTF-8 bytes.</exception>
    public static string EncryptPdfPassword(string password, EncryptionKey key)
    {
        ArgumentNullException.ThrowIfNull(key);
        Text.CheckText(password, UrlBuildErrorCode.InvalidPassword, "password");
        if (Text.Utf8.GetByteCount(password) > XtandardSpec.MaxPdfPasswordBytes)
        {
            throw new UrlBuildException(UrlBuildErrorCode.InvalidPassword, $"password is longer than {XtandardSpec.MaxPdfPasswordBytes} bytes");
        }

        return key.Seal(password);
    }
}
