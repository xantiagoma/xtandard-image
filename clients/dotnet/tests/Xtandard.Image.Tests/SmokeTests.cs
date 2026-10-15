using System.Net;

namespace Xtandard.Image.Tests;

/// <summary>
/// Smoke tests against a running server, skipped unless XI_SMOKE_URL is set. The playground demo
/// (compose.playground.yml, unsigned URLs allowed) works: <c>XI_SMOKE_URL=http://127.0.0.1:8080 task test</c>.
/// They need the demo's local fixtures (xi://local/…) and, for /video, an image with video enabled.
/// </summary>
public sealed class SmokeTests
{
    private static readonly string BaseUrl = Environment.GetEnvironmentVariable("XI_SMOKE_URL") ?? string.Empty;
    private static readonly string ImageSource = Sources.XiSource("local", "jpeg/baseline.jpg");
    private static readonly string VideoSource = Sources.XiSource("local", "video/bbb-h264-aac.mp4");

    private static XtandardClient Client(SourceEncoding encoding = SourceEncoding.Base64)
    {
        Assert.SkipWhen(BaseUrl.Length == 0, "set XI_SMOKE_URL to run against a server");
        return new XtandardClient(new XtandardClientOptions { BaseUrl = BaseUrl, SourceEncoding = encoding });
    }

    [Fact]
    public async Task HealthImageAndInfo()
    {
        using var xi = Client(SourceEncoding.Plain);
        var ct = TestContext.Current.CancellationToken;
        await xi.HealthzAsync(ct);
        Assert.True((await xi.ReadyzAsync(ct)).Ready);
        using (var res = await xi.ImageAsync(ImageSource, new() { new ProcessingOption.Resize("fit", 64, 64) }, "webp", cancellationToken: ct))
        {
            Assert.Equal("image/webp", res.Content.Headers.ContentType!.MediaType);
        }

        var info = await xi.InfoAsync(ImageSource, new() { { "size", true }, { "format", true }, { "dimensions", true } }, cancellationToken: ct);
        Assert.Equal("jpeg", info.Format);
        Assert.True(info.Width > 0);
        await Assert.ThrowsAsync<NotFoundException>(() => xi.ImageAsync(Sources.XiSource("local", "jpeg/missing.jpg"), cancellationToken: ct));
    }

    [Fact]
    public async Task QueryDialect()
    {
        using var xi = Client();
        var url = xi.QueryUrls.Url(ImageSource, new() { { QueryKeys.W, 32 }, { QueryKeys.F, "png" } });
        using var res = await xi.MediaAsync(url, cancellationToken: TestContext.Current.CancellationToken);
        Assert.Equal("image/png", res.Content.Headers.ContentType!.MediaType);
    }

    [Fact]
    public async Task VideoRangeHeadAndStream()
    {
        using var xi = Client();
        var ct = TestContext.Current.CancellationToken;
        var options = new VideoOptions { new VideoOption.Duration(1), new VideoOption.Width(160) };
        using (var res = await xi.VideoAsync(VideoSource, options, "webm", request: new() { Range = ByteRange.Of(0, 99) }, cancellationToken: ct))
        {
            Assert.Contains(res.StatusCode, new[] { HttpStatusCode.OK, HttpStatusCode.PartialContent });
        }

        using (var head = await xi.VideoAsync(VideoSource, options, "webm", request: new() { Method = HttpMethod.Head }, cancellationToken: ct))
        {
            Assert.Equal("video/webm", head.Content.Headers.ContentType!.MediaType);
        }

        var m = await xi.StreamAsync(VideoSource, StreamProtocol.Hls, [240], cancellationToken: ct);
        Assert.StartsWith("#EXTM3U", m.Body, StringComparison.Ordinal);
        var child = m.Body.Split('\n').First(l => l.Length > 0 && !l.StartsWith('#'));
        using var part = await xi.MediaAsync(child.Trim(), cancellationToken: ct);
        Assert.Equal(HttpStatusCode.OK, part.StatusCode);
    }
}
