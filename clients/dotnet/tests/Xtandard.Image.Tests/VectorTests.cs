using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace Xtandard.Image.Tests;

/// <summary>testdata/security/vectors.json: the signing, enc/ and pdf_password vectors shared with Go and TypeScript.</summary>
public sealed class VectorTests
{
    private static JsonElement V => Fixtures.Vectors.Value;

    public static TheoryData<string, int> Of(string section)
    {
        var data = new TheoryData<string, int>();
        var i = 0;
        foreach (var v in V.GetProperty(section).EnumerateArray())
        {
            data.Add(v.GetProperty("name").GetString()!, i++);
        }

        return data;
    }

    internal static string Decrypt(string token, string keyHex)
    {
        var b64 = token.Replace('-', '+').Replace('_', '/');
        b64 += new string('=', (4 - (b64.Length % 4)) % 4);
        var raw = Convert.FromBase64String(b64);
        using var aes = Aes.Create();
        aes.Key = Convert.FromHexString(keyHex);
        return Encoding.UTF8.GetString(aes.DecryptCbc(raw.AsSpan(16), raw.AsSpan(0, 16), PaddingMode.PKCS7));
    }

    [Theory]
    [MemberData(nameof(Of), "imgproxy")]
    public void Imgproxy(string name, int index)
    {
        _ = name;
        var v = V.GetProperty("imgproxy")[index];
        var size = v.GetProperty("size").GetInt32();
        var path = v.Str("path")!;
        var signature = v.Str("signature")!;
        var k = new SigningKey(v.Str("key")!, v.Str("salt")!, size);
        Assert.Equal(signature, Signing.Sign(path, k));
        var p = path.StartsWith('/') ? path : "/" + path;
        Assert.Equal($"/{signature}{p}", Signing.SignImgproxyPath(path, k));
        var raw = new SigningKey(Convert.FromHexString(v.Str("key")!), Convert.FromHexString(v.Str("salt")!), size);
        Assert.Equal(signature, Signing.Sign(path, raw));
    }

    [Theory]
    [MemberData(nameof(Of), "xi_sig")]
    public void XiSig(string name, int index)
    {
        _ = name;
        var v = V.GetProperty("xi_sig")[index];
        var k = new SigningKey(v.Str("key")!, v.Str("salt")!, v.GetProperty("size").GetInt32());
        var path = v.Str("path")!;
        var signedQuery = v.Str("signed_query")!;
        Assert.Equal(signedQuery, Signing.SignXiQuery(path, v.Str("query")!, k));
        Assert.Equal(signedQuery, Signing.SignXiQuery(path, signedQuery, k));
        Assert.Equal(
            $"https://img.example.com{path}?{signedQuery}",
            Signing.SignXiUrl($"https://img.example.com{path}?{v.Str("query")}", k));
    }

    [Theory]
    [MemberData(nameof(Of), "encryption")]
    public void EncryptionVector(string name, int index)
    {
        _ = name;
        var v = V.GetProperty("encryption")[index];
        var token = Encryption.EncryptSource(v.Str("plaintext")!, new EncryptionKey(v.Str("key")!, v.Str("iv")));
        Assert.Equal(v.Str("token"), token);
        Assert.Equal(v.Str("plaintext"), Decrypt(token, v.Str("key")!));
    }

    [Theory]
    [MemberData(nameof(Of), "pdf_password")]
    public void PdfPassword(string name, int index)
    {
        _ = name;
        var v = V.GetProperty("pdf_password")[index];
        Assert.Equal(v.Str("token"), Encryption.EncryptPdfPassword(v.Str("password")!, new EncryptionKey(v.Str("key")!, v.Str("iv"))));
    }

    [Fact]
    public void RandomIv()
    {
        var keyHex = V.GetProperty("pdf_password")[0].Str("key")!;
        var k = new EncryptionKey(keyHex);
        var a = Encryption.EncryptPdfPassword("s3cret", k);
        var b = Encryption.EncryptPdfPassword("s3cret", k);
        Assert.NotEqual(a, b);
        Assert.Equal("s3cret", Decrypt(a, keyHex));
        Assert.Equal("s3cret", Decrypt(b, keyHex));
    }

    [Fact]
    public void XiMessage()
    {
        Assert.Equal(("s", "/?url=x&w=1"), Signing.XiMessage("/", "url=x&&xi_sig=s&w=1"));
        Assert.Equal(((string?)null, "/"), Signing.XiMessage("/", string.Empty));
        var e = Assert.Throws<UrlBuildException>(() => Signing.XiMessage("/", "xi_sig=a&xi_sig=b"));
        Assert.Equal(UrlBuildErrorCode.InvalidRequest, e.Code);
    }

    [Fact]
    public void KeysHideTheirMaterial()
    {
        var s = new SigningKey(new string('0', 64), new string('1', 64)).ToString();
        Assert.DoesNotContain("00", s, StringComparison.Ordinal);
        Assert.DoesNotContain("11", s, StringComparison.Ordinal);
        var e = new EncryptionKey(new string('a', 64), new string('b', 32)).ToString();
        Assert.DoesNotContain("aa", e, StringComparison.Ordinal);
        Assert.DoesNotContain("bb", e, StringComparison.Ordinal);
    }
}
