using static Xtandard.Image.ProcessingOption;

namespace Xtandard.Image.Tests;

/// <summary>The README's builder examples compile and agree with each other.</summary>
public sealed class ReadmeTests
{
    private static readonly SigningKey Key = new("943b421c9eb07c830af81030552c86009268de4e532ba2ee2eab8247c6da0881", "520f986b998545b4785e0defbc4f3c1203f22de2374a3d53cb7a7fe9fea309c5");

    [Fact]
    public void UrlBuilder()
    {
        var img = new ImgproxyUrlBuilder(new ImgproxyConfig
        {
            BaseUrl = "https://img.example.com",
            Signing = Key,
            Encryption = new EncryptionKey("1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1"),
        });

        var a = img.Url("s3://photos/cat.jpg", new() { { "rs", "fill", 1200, 630 }, { "g", "sm" }, { "q", 80 } }, extension: "webp");
        var b = img.Url("s3://photos/cat.jpg", new() { new Resize("fill", 1200, 630), new Gravity("sm"), new Quality(80) }, extension: "webp");
        Assert.Equal(a, b);
        Assert.EndsWith("/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp", a, StringComparison.Ordinal);

        Assert.Contains("/rs:fit:300:300/bl:2/", img.Image(Sources.XiSource("photos", "2026/cat.jpg")).Resize("fit", 300, 300).Blur(2).Extension("png").Build(), StringComparison.Ordinal);
        Assert.Equal(
            img.ChainedUrl("s3://photos/cat.jpg", [new() { { "rs", "fit", 500, 500 } }, new() { { "bl", 2 } }]),
            img.Image("s3://photos/cat.jpg").Resize("fit", 500, 500).Pipeline().Blur(2).Build());
        Assert.Contains("/plain/https://example.com/a%20b.jpg", img.Url("https://example.com/a b.jpg", new() { { "w", 300 } }, sourceEncoding: SourceEncoding.Plain), StringComparison.Ordinal);
        Assert.Contains("/enc/", img.Url("s3://private/cat.jpg", new() { { "w", 300 } }, sourceEncoding: SourceEncoding.Encrypted), StringComparison.Ordinal);
        Assert.Contains("/pdp:", img.Url("s3://docs/report.pdf", new() { { "rs", "fit", 800, 0 } }, extension: "png", pdfPassword: "s3cret"), StringComparison.Ordinal);
        Assert.Contains("/info/", img.InfoUrl("s3://photos/cat.jpg", new() { { "size", true }, { "format", true }, { "dimensions", true } }), StringComparison.Ordinal);
        Assert.Contains("/rs:fill:640:360/vs:12/vd:8/", img.VideoUrl("s3://media/talk.mov", new() { { "rs", "fill", 640, 360 }, { "vs", 12 }, { "vd", 8 } }, extension: "webm"), StringComparison.Ordinal);
        Assert.Contains("/vs:12/vd:8/mu:1/", img.Video("s3://media/talk.mov").Start(12).Duration(8).Mute(true).Extension("mp4").Build(), StringComparison.Ordinal);
        Assert.Contains("/st:hls:360:720/sgd:4/", img.StreamUrl("s3://media/talk.mov", StreamProtocol.Hls, renditions: [360, 720], segmentDuration: 4), StringComparison.Ordinal);
    }

    [Fact]
    public void QueryDialect()
    {
        var q = new QueryUrlBuilder(new QueryConfig { BaseUrl = "https://img.example.com", Signing = Key });
        var url = q.Url("s3://photos/cat.jpg", new() { { QueryKeys.W, 300 }, { QueryKeys.F, "webp" } }, new() { new Blur(2) });
        Assert.StartsWith("https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=", url, StringComparison.Ordinal);
        Assert.Equal(url, Signing.SignXiUrl(url, Key, mount: "/q"));
        Assert.Contains("&w=300&o=bl:2&xi_sig=", q.Image("s3://photos/cat.jpg").Param("w", 300).Blur(2).Build(), StringComparison.Ordinal);
        Assert.Contains("/q/video?url=s3://media/talk.mov&rs=fill,640,360&vs=1&format=webm&xi_sig=", q.VideoUrl("s3://media/talk.mov", new() { { "rs", "fill", 640, 360 }, { "vs", 1 } }, extension: "webm"), StringComparison.Ordinal);
        Assert.Contains("&st=hls,360&format=m3u8&xi_sig=", q.StreamUrl("s3://media/talk.mov", StreamProtocol.Hls, renditions: [360]), StringComparison.Ordinal);
        Assert.Contains("/q/resize?url=s3://photos/cat.jpg&width=300&xi_sig=", q.ImaginaryUrl(ImaginaryEndpoints.Resize, "s3://photos/cat.jpg", new() { { "width", 300 } }), StringComparison.Ordinal);
    }
}
