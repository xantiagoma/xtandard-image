using System.Net;
using System.Text;
using System.Text.RegularExpressions;
using Microsoft.Extensions.DependencyInjection;

namespace Xtandard.Image.Tests;

/// <summary>A stand-in server: answers requests from a function and records them.</summary>
internal sealed class StandIn : HttpMessageHandler
{
    private readonly Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> answer;

    internal StandIn(Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> answer) => this.answer = answer;

    internal List<HttpRequestMessage> Seen { get; } = [];

    protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
    {
        Seen.Add(request);
        return answer(request, cancellationToken);
    }
}

/// <summary>The HTTP client against a stand-in server.</summary>
public sealed partial class ClientTests
{
    private const string Base = "http://xi.test";
    private static readonly SigningKey Key = new("736563726574", "68656C6C6F");
    private static readonly byte[] Body = [.. Enumerable.Range(0, 1000).Select(i => (byte)(i % 256))];
    private static readonly string VideoUrl = new ImgproxyUrlBuilder(new() { BaseUrl = Base, Signing = Key })
        .VideoUrl("s3://m/clip.mov", new() { { "vd", 2 } }, "webm");

    private static HttpResponseMessage Json(HttpStatusCode status, string json) =>
        new(status) { Content = new StringContent(json, Encoding.UTF8, "application/json") };

    private static HttpResponseMessage Bytes(HttpStatusCode status, byte[] body, string type)
    {
        var res = new HttpResponseMessage(status) { Content = new ByteArrayContent(body) };
        res.Content.Headers.TryAddWithoutValidation("Content-Type", type);
        return res;
    }

    [GeneratedRegex(@"^bytes=(\d+)-(\d*)$")]
    private static partial Regex RangeRe();

    private static Task<HttpResponseMessage> Server(HttpRequestMessage req, CancellationToken ct)
    {
        var path = req.RequestUri!.AbsolutePath;
        var head = req.Method == HttpMethod.Head;
        HttpResponseMessage res;
        if (path == "/healthz")
        {
            res = new(HttpStatusCode.OK) { Content = new StringContent("ok\n") };
        }
        else if (path == "/readyz")
        {
            res = new(HttpStatusCode.ServiceUnavailable) { Content = new StringContent("draining\n") };
        }
        else if (path.StartsWith("/info/", StringComparison.Ordinal))
        {
            res = Json(HttpStatusCode.OK, """{"width":640,"height":480,"format":"jpeg"}""");
        }
        else if (path.StartsWith("/video/", StringComparison.Ordinal) && path.EndsWith(".m3u8", StringComparison.Ordinal))
        {
            res = new(HttpStatusCode.OK) { Content = new StringContent("#EXTM3U\n/_stream/T/v0.m3u8\n", Encoding.UTF8, "application/vnd.apple.mpegurl") };
        }
        else if (path.StartsWith("/video/", StringComparison.Ordinal) || path.StartsWith("/_stream/", StringComparison.Ordinal))
        {
            if (path.StartsWith("/video/", StringComparison.Ordinal) && req.RequestUri.OriginalString != VideoUrl)
            {
                res = Json(HttpStatusCode.Forbidden, """{"error":{"kind":"unauthorized","message":"invalid signature"}}""");
            }
            else if (req.Headers.TryGetValues("Range", out var ranges) && RangeRe().Match(ranges.Single()) is { Success: true } m)
            {
                var start = int.Parse(m.Groups[1].Value, System.Globalization.CultureInfo.InvariantCulture);
                var end = m.Groups[2].Value.Length > 0 ? int.Parse(m.Groups[2].Value, System.Globalization.CultureInfo.InvariantCulture) : Body.Length - 1;
                res = Bytes(HttpStatusCode.PartialContent, head ? [] : Body[start..(end + 1)], "video/webm");
                res.Content.Headers.TryAddWithoutValidation("Content-Range", $"bytes {start}-{end}/{Body.Length}");
            }
            else
            {
                res = Bytes(HttpStatusCode.OK, head ? [] : Body, "video/webm");
            }
        }
        else if (path == "/_admin/play/api/meta")
        {
            res = Json(HttpStatusCode.OK, """{"version":"test"}""");
        }
        else if (path == "/proxy-error")
        {
            res = new(HttpStatusCode.BadGateway) { Content = new StringContent("<html>bad gateway</html>") };
        }
        else if (path == "/busy")
        {
            res = Json(HttpStatusCode.TooManyRequests, """{"error":{"kind":"overloaded","message":"too many requests","request_id":"r1"}}""");
        }
        else if (path == "/down")
        {
            throw new HttpRequestException("refused");
        }
        else if (path == "/slow")
        {
            return Task.Delay(Timeout.Infinite, ct).ContinueWith<HttpResponseMessage>(_ => throw new TaskCanceledException(), CancellationToken.None, TaskContinuationOptions.None, TaskScheduler.Default);
        }
        else
        {
            res = Json(HttpStatusCode.NotFound, """{"error":{"kind":"not_found","message":"source image not found"}}""");
        }

        res.RequestMessage = req;
        return Task.FromResult(res);
    }

    private static (XtandardClient Client, StandIn Server) Make(TimeSpan? timeout = null)
    {
        var server = new StandIn(Server);
        var http = new HttpClient(server) { Timeout = timeout ?? TimeSpan.FromSeconds(30) };
        var client = new XtandardClient(http, new XtandardClientOptions { BaseUrl = Base, Signing = Key, PlaygroundPath = "/_admin/play/" });
        return (client, server);
    }

    [Fact]
    public async Task Health()
    {
        var (xi, _) = Make();
        await xi.HealthzAsync(TestContext.Current.CancellationToken);
        var r = await xi.ReadyzAsync(TestContext.Current.CancellationToken);
        Assert.Equal(new Readiness(false, "draining"), r);
    }

    [Fact]
    public async Task Info()
    {
        var (xi, server) = Make();
        var info = await xi.InfoAsync("s3://b/cat.jpg", new() { { "size", true }, { "format", true } }, cancellationToken: TestContext.Current.CancellationToken);
        Assert.Equal(640, info.Width);
        var req = server.Seen[^1];
        Assert.Equal("application/json", req.Headers.Accept.Single().MediaType);
        Assert.StartsWith("/info/", req.RequestUri!.AbsolutePath, StringComparison.Ordinal);
        var again = await xi.InfoAtAsync(req.RequestUri.AbsolutePath, TestContext.Current.CancellationToken);
        Assert.Equal("jpeg", again.Format);
    }

    [Fact]
    public async Task VideoWholeRangeAndHead()
    {
        var (xi, server) = Make();
        var ct = TestContext.Current.CancellationToken;
        using (var res = await xi.VideoAsync("s3://m/clip.mov", new() { { "vd", 2 } }, "webm", cancellationToken: ct))
        {
            Assert.Equal(Body, await res.Content.ReadAsByteArrayAsync(ct));
        }

        using (var res = await xi.VideoAsync("s3://m/clip.mov", new() { { "vd", 2 } }, "webm", request: new() { Range = ByteRange.Of(10, 19) }, cancellationToken: ct))
        {
            Assert.Equal(HttpStatusCode.PartialContent, res.StatusCode);
            Assert.Equal("bytes 10-19/1000", res.Content.Headers.GetValues("Content-Range").Single());
            Assert.Equal(Body[10..20], await res.Content.ReadAsByteArrayAsync(ct));
            Assert.Equal("bytes=10-19", server.Seen[^1].Headers.GetValues("Range").Single());
        }

        using (var res = await xi.VideoAsync("s3://m/clip.mov", new() { { "vd", 2 } }, "webm", request: new() { Method = HttpMethod.Head }, cancellationToken: ct))
        {
            Assert.Equal(HttpMethod.Head, server.Seen[^1].Method);
            Assert.Equal("video/webm", res.Content.Headers.ContentType!.MediaType);
        }

        using (var res = await xi.VideoAsync("s3://m/clip.mov", new() { { "vd", 2 } }, "webm", request: new() { Range = ByteRange.Suffix(100) }, cancellationToken: ct))
        {
            Assert.Equal("bytes=-100", server.Seen[^1].Headers.GetValues("Range").Single());
        }
    }

    [Fact]
    public async Task StreamedBody()
    {
        var (xi, _) = Make();
        var ct = TestContext.Current.CancellationToken;
        using var res = await xi.VideoAsync("s3://m/clip.mov", new() { { "vd", 2 } }, "webm", request: new() { Stream = true }, cancellationToken: ct);
        await using var stream = await res.Content.ReadAsStreamAsync(ct);
        using var buf = new MemoryStream();
        await stream.CopyToAsync(buf, ct);
        Assert.Equal(Body, buf.ToArray());
    }

    [Fact]
    public async Task StreamManifestAndTicketChild()
    {
        var (xi, server) = Make();
        var ct = TestContext.Current.CancellationToken;
        var m = await xi.StreamAsync("s3://m/clip.mov", StreamProtocol.Hls, [360], cancellationToken: ct);
        Assert.StartsWith("#EXTM3U", m.Body, StringComparison.Ordinal);
        Assert.StartsWith("application/vnd.apple.mpegurl", m.ContentType, StringComparison.Ordinal);
        Assert.Equal(m.Url, server.Seen[^1].RequestUri!.OriginalString);
        Assert.Contains("application/vnd.apple.mpegurl", string.Join(",", server.Seen[^1].Headers.GetValues("Accept")), StringComparison.Ordinal);
        var child = m.Body.Split('\n').First(l => l.Length > 0 && !l.StartsWith('#'));
        using var part = await xi.MediaAsync(child, cancellationToken: ct);
        Assert.Equal(HttpStatusCode.OK, part.StatusCode);
        Assert.Equal($"{Base}/_stream/T/v0.m3u8", server.Seen[^1].RequestUri!.OriginalString);
    }

    [Fact]
    public async Task PlaygroundMeta()
    {
        var (xi, _) = Make();
        var meta = await xi.PlaygroundMetaAsync(TestContext.Current.CancellationToken);
        Assert.Equal("test", meta.GetProperty("version").GetString());
    }

    [Fact]
    public async Task ErrorsByStatus()
    {
        var (xi, _) = Make();
        var ct = TestContext.Current.CancellationToken;
        var nf = await Assert.ThrowsAsync<NotFoundException>(() => xi.ImageAsync("s3://b/missing.jpg", cancellationToken: ct));
        Assert.Equal((HttpStatusCode.NotFound, "not_found", "source image not found", null), (nf.Status, nf.Kind, nf.Message, nf.RequestId));

        var busy = await Assert.ThrowsAsync<TooManyRequestsException>(() => xi.MediaAsync("/busy", cancellationToken: ct));
        Assert.Equal(("overloaded", "r1"), (busy.Kind, busy.RequestId));

        var proxy = await Assert.ThrowsAsync<GatewayException>(() => xi.MediaAsync("/proxy-error", cancellationToken: ct));
        Assert.Equal(("unknown", "<html>bad gateway</html>"), (proxy.Kind, proxy.Message));
        Assert.IsAssignableFrom<ServerErrorException>(proxy);

        var forbidden = await Assert.ThrowsAsync<ForbiddenException>(() => xi.MediaAsync(VideoUrl + "x", cancellationToken: ct));
        Assert.Equal("unauthorized", forbidden.Kind);

        await Assert.ThrowsAsync<XtandardTransportException>(() => xi.MediaAsync("/down", cancellationToken: ct));
        await Assert.ThrowsAsync<UrlBuildException>(() => xi.ImageAsync(string.Empty, cancellationToken: ct));
    }

    [Fact]
    public async Task TimeoutIsATransportErrorAndCancellationIsNot()
    {
        var (xi, _) = Make(TimeSpan.FromMilliseconds(50));
        var e = await Assert.ThrowsAsync<XtandardTransportException>(() => xi.MediaAsync("/slow", cancellationToken: TestContext.Current.CancellationToken));
        Assert.IsAssignableFrom<OperationCanceledException>(e.InnerException);

        var (xi2, _) = Make();
        using var cts = new CancellationTokenSource();
        var pending = xi2.MediaAsync("/slow", cancellationToken: cts.Token);
        await cts.CancelAsync();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => pending);
    }

    [Fact]
    public async Task UrlsAreSentByteForByte()
    {
        var (xi, server) = Make();
        var url = xi.Urls.Url("https://example.com/a b/../c%2Fd.jpg?x=1", sourceEncoding: SourceEncoding.Plain);
        await Assert.ThrowsAsync<NotFoundException>(() => xi.MediaAsync(url, cancellationToken: TestContext.Current.CancellationToken));
        Assert.Equal(url, server.Seen[^1].RequestUri!.OriginalString);
        Assert.Contains("/plain/https://example.com/a%20b/../c%252Fd.jpg%3Fx=1", server.Seen[^1].RequestUri!.AbsoluteUri, StringComparison.Ordinal);
    }

    [Fact]
    public async Task HeadersAndAccept()
    {
        var server = new StandIn(Server);
        var options = new XtandardClientOptions { BaseUrl = Base };
        options.Headers["X-Tenant"] = "a";
        var xi = new XtandardClient(new HttpClient(server), options);
        await Assert.ThrowsAsync<NotFoundException>(() => xi.ImageAsync(
            "s3://b/cat.jpg",
            new() { { "w", 10 } },
            request: new() { Accept = "image/avif,image/webp", Headers = new Dictionary<string, string> { ["X-Trace"] = "t" } },
            cancellationToken: TestContext.Current.CancellationToken));
        var req = server.Seen[^1];
        Assert.Equal("a", req.Headers.GetValues("X-Tenant").Single());
        Assert.Equal("t", req.Headers.GetValues("X-Trace").Single());
        Assert.Equal("image/avif,image/webp", string.Join(',', req.Headers.GetValues("Accept")));
    }

    [Fact]
    public async Task DependencyInjection()
    {
        var server = new StandIn(Server);
        var services = new ServiceCollection();
        services.AddXtandardImage(o =>
        {
            o.BaseUrl = Base;
            o.Signing = Key;
        }).ConfigurePrimaryHttpMessageHandler(() => server);
        await using var sp = services.BuildServiceProvider();
        var urls = sp.GetRequiredService<ImgproxyUrlBuilder>();
        Assert.Equal(VideoUrl, urls.VideoUrl("s3://m/clip.mov", new() { { "vd", 2 } }, "webm"));
        Assert.Same(urls, sp.GetRequiredService<ImgproxyUrlBuilder>());
        Assert.StartsWith($"{Base}/q/?url=", sp.GetRequiredService<QueryUrlBuilder>().Url("s3://b/a.jpg"), StringComparison.Ordinal);
        var xi = sp.GetRequiredService<XtandardClient>();
        using var res = await xi.VideoAsync("s3://m/clip.mov", new() { { "vd", 2 } }, "webm", cancellationToken: TestContext.Current.CancellationToken);
        Assert.Equal(HttpStatusCode.OK, res.StatusCode);
        Assert.Single(server.Seen);
    }

    [Fact]
    public void OwnedClientIsDisposed()
    {
        var xi = new XtandardClient(new XtandardClientOptions { BaseUrl = Base });
        xi.Dispose();
        xi.Dispose();
        Assert.Equal($"{Base}/insecure/czM6Ly9iL2EuanBn", xi.Urls.Url("s3://b/a.jpg"));
    }
}
