using System.Text.Json;

namespace Xtandard.Image.Tests;

/// <summary>
/// clients/spec/conformance.json: the cases every official client runs (ADR 0013). Positive cases
/// must produce expect.value byte for byte; error cases must throw <see cref="UrlBuildException"/> with
/// expect.error as its code. Each case is built twice where both forms exist: through options
/// collections and through the fluent builders (<c>Option(name, args)</c>), and both must agree.
/// </summary>
public sealed class ConformanceTests
{
    private static JsonElement Keys => Fixtures.Conformance.Value.GetProperty("keys");

    private static JsonElement CaseAt(int i) => Fixtures.Conformance.Value.GetProperty("cases")[i];

    public static TheoryData<int, string> Cases()
    {
        var data = new TheoryData<int, string>();
        var i = 0;
        foreach (var c in Fixtures.Conformance.Value.GetProperty("cases").EnumerateArray())
        {
            data.Add(i++, $"{c.GetProperty("op").GetString()}:{c.GetProperty("name").GetString()}");
        }

        return data;
    }

    [Fact]
    public void FixtureCoversEveryOp()
    {
        var cases = Fixtures.Conformance.Value.GetProperty("cases").EnumerateArray().ToList();
        Assert.True(cases.Count >= 100);
        Assert.True(cases.Select(c => c.GetProperty("op").GetString()).Distinct().Count() >= 17);
    }

    [Theory]
    [MemberData(nameof(Cases))]
    public void Case(int index, string name)
    {
        _ = name;
        var c = CaseAt(index);
        if (SkipReason(c) is { } reason)
        {
            Assert.Skip(reason);
        }

        var expect = c.GetProperty("expect");
        if (expect.Str("error") is { } code)
        {
            var ex = Assert.Throws<UrlBuildException>(() => RunMapping(c));
            Assert.Equal(code, ex.CodeName);
            if (HasFluent(c))
            {
                var ex2 = Assert.Throws<UrlBuildException>(() => RunFluent(c));
                Assert.Equal(code, ex2.CodeName);
            }

            return;
        }

        var want = expect.GetProperty("value").GetString();
        Assert.Equal(want, RunMapping(c));
        if (HasFluent(c))
        {
            Assert.Equal(want, RunFluent(c));
        }
    }

    /// <summary>Cases a statically typed API cannot express (clients/README.md, "Conformance cases").</summary>
    private static string? SkipReason(JsonElement c)
    {
        var op = c.GetProperty("op").GetString();
        var input = c.GetProperty("input");
        var pipelines = input.Has("options") ? input.GetProperty("options").GetArrayLength() : 0;
        return op switch
        {
            "imgproxy.video_url" when input.Has("pdf_password") =>
                "not expressible: VideoUrl() and the /video builder have no PDF password parameter",
            "imgproxy.info_url" or "imgproxy.video_url" when pipelines > 1 =>
                "not expressible: InfoUrl()/VideoUrl() take one options collection and their builders have no Pipeline()",
            _ => null,
        };
    }

    private static SigningKey? Signing(JsonElement cfg)
    {
        var s = cfg.Get("signing");
        if (s.ValueKind is JsonValueKind.Undefined or JsonValueKind.Null or JsonValueKind.False)
        {
            return null;
        }

        var k = s.ValueKind == JsonValueKind.True ? Keys.GetProperty("signing") : s;
        var size = cfg.Has("signature_size") ? cfg.GetProperty("signature_size").GetInt32() : XtandardSpec.DefaultSignatureSize;
        return new SigningKey(k.Str("key")!, k.Str("salt")!, size);
    }

    private static EncryptionKey? Encryption(JsonElement cfg)
    {
        var e = cfg.Get("encryption");
        if (e.ValueKind is JsonValueKind.Undefined or JsonValueKind.Null or JsonValueKind.False)
        {
            return null;
        }

        var k = e.ValueKind == JsonValueKind.True ? Keys.GetProperty("encryption") : e;
        return new EncryptionKey(k.Str("key")!, k.Str("iv"));
    }

    private static ImgproxyUrlBuilder Imgproxy(JsonElement cfg)
    {
        var c = new ImgproxyConfig { Signing = Signing(cfg), Encryption = Encryption(cfg) };
        if (cfg.Str("base_url") is { } b)
        {
            c = c with { BaseUrl = b };
        }

        if (cfg.Str("unsigned_signature") is { } u)
        {
            c = c with { UnsignedSignature = u };
        }

        if (cfg.Str("source_encoding") is { } enc)
        {
            c = c with { SourceEncoding = Fixtures.Encoding(enc) };
        }

        if (cfg.Has("split_source"))
        {
            c = c with { SplitSource = cfg.GetProperty("split_source").GetInt32() };
        }

        if (cfg.Str("separator") is { } sep)
        {
            c = c with { Separator = sep };
        }

        if (cfg.Str("option_names") is { } style)
        {
            c = c with { OptionNames = Fixtures.Style(style) };
        }

        return new ImgproxyUrlBuilder(c);
    }

    private static QueryUrlBuilder Query(JsonElement cfg)
    {
        var c = new QueryConfig { Signing = Signing(cfg), Encryption = Encryption(cfg) };
        if (cfg.Str("base_url") is { } b)
        {
            c = c with { BaseUrl = b };
        }

        if (cfg.Str("mount") is { } m)
        {
            c = c with { Mount = m };
        }

        if (cfg.Str("separator") is { } sep)
        {
            c = c with { Separator = sep };
        }

        if (cfg.Str("option_names") is { } style)
        {
            c = c with { OptionNames = Fixtures.Style(style) };
        }

        return new QueryUrlBuilder(c);
    }

    private static List<T> Pipelines<T>(JsonElement input)
        where T : IEnumerable<OptionEntry>, new()
    {
        var o = input.Get("options");
        if (o.ValueKind != JsonValueKind.Array)
        {
            return [];
        }

        var list = new List<T>();
        foreach (var pl in o.EnumerateArray())
        {
            var opts = new T();
            foreach (var entry in pl.EnumerateArray())
            {
                Add(opts, entry[0].GetString()!, Fixtures.Args(entry[1]));
            }

            list.Add(opts);
        }

        return list;
    }

    private static void Add<T>(T options, string name, OptionArg[] args)
    {
        switch (options)
        {
            case ProcessingOptions p:
                p.Add(name, args);
                break;
            case InfoOptions i:
                i.Add(name, args);
                break;
            case VideoOptions v:
                v.Add(name, args);
                break;
            default:
                throw new InvalidOperationException(typeof(T).Name);
        }
    }

    private static T? One<T>(JsonElement input)
        where T : class, IEnumerable<OptionEntry>, new()
    {
        var p = Pipelines<T>(input);
        return p.Count switch
        {
            0 => null,
            1 => p[0],
            _ => throw new InvalidOperationException("one pipeline expected"),
        };
    }

    private static QueryParams Params(JsonElement input)
    {
        var q = new QueryParams();
        if (input.Get("params") is { ValueKind: JsonValueKind.Array } ps)
        {
            foreach (var p in ps.EnumerateArray())
            {
                q.Add(p[0].GetString()!, Fixtures.Arg(p[1]));
            }
        }

        return q;
    }

    private static SourceEncoding? Encoding(JsonElement input) => input.Str("source_encoding") is { } e ? Fixtures.Encoding(e) : null;

    private static int[]? Renditions(JsonElement input) =>
        input.Get("renditions") is { ValueKind: JsonValueKind.Array } r ? [.. r.EnumerateArray().Select(x => x.GetInt32())] : null;

    private static int? SegmentDuration(JsonElement input) => input.Has("segment_duration") ? input.GetProperty("segment_duration").GetInt32() : null;

    private static SegmentFormat? SegmentFormatOf(JsonElement input) => input.Str("segment_format") is { } f ? (SegmentFormat?)SegmentFormat.FromString(f) : null;

    private static ByteRange Range(JsonElement r)
    {
        if (r.ValueKind == JsonValueKind.String)
        {
            return ByteRange.FromHeader(r.GetString()!);
        }

        if (r.Has("suffix"))
        {
            return ByteRange.Suffix(r.GetProperty("suffix").GetInt64());
        }

        return ByteRange.Of(r.GetProperty("start").GetInt64(), r.Has("end") ? r.GetProperty("end").GetInt64() : null);
    }

    /// <summary>The case through options collections.</summary>
    private static string RunMapping(JsonElement c) => RunMapping(c, c.GetProperty("op").GetString()!);

    /// <summary>The case as op (query.query_request runs its input.build op first).</summary>
    private static string RunMapping(JsonElement c, string op)
    {
        var cfg = c.Get("config");
        var input = c.GetProperty("input");
        var source = input.Str("source")!;
        switch (op)
        {
            case "query.query_request":
                {
                    var q = QueryMethodRequest.FromUrl(RunMapping(c, input.Str("build")!));
                    return $"{q.Method} {q.Url}\nContent-Type: {q.ContentType}\n\n{q.Body}";
                }

            case "imgproxy.url":
                {
                    var pipelines = Pipelines<ProcessingOptions>(input);
                    var b = Imgproxy(cfg);
                    return pipelines.Count == 1
                        ? b.Url(source, pipelines[0], input.Str("extension"), Encoding(input), input.Str("pdf_password"))
                        : b.ChainedUrl(source, pipelines, input.Str("extension"), Encoding(input), input.Str("pdf_password"));
                }

            case "imgproxy.info_url":
                return Imgproxy(cfg).InfoUrl(source, One<InfoOptions>(input), Encoding(input), input.Str("pdf_password"));
            case "imgproxy.video_url":
                return Imgproxy(cfg).VideoUrl(source, One<VideoOptions>(input), input.Str("extension"), Encoding(input));
            case "imgproxy.stream_url":
                return Imgproxy(cfg).StreamUrl(
                    source, input.Str("protocol")!, Renditions(input), SegmentDuration(input), SegmentFormatOf(input), One<VideoOptions>(input), Encoding(input));
            case "query.url":
                {
                    var pipelines = Pipelines<ProcessingOptions>(input);
                    var q = Query(cfg);
                    return pipelines.Count == 1
                        ? q.Url(source, Params(input), pipelines[0], input.Str("extension"), input.Str("pdf_password"))
                        : q.ChainedUrl(source, Params(input), pipelines, input.Str("extension"), input.Str("pdf_password"));
                }

            case "query.video_url":
                {
                    // Several pipelines are written in order into one /q/video URL: one collection holds them all.
                    var all = new VideoOptions();
                    foreach (var e in Pipelines<VideoOptions>(input).SelectMany(p => p))
                    {
                        all.Add(e);
                    }

                    return Query(cfg).VideoUrl(source, all, input.Str("extension"));
                }
            case "query.stream_url":
                return Query(cfg).StreamUrl(
                    source, input.Str("protocol")!, Renditions(input), SegmentDuration(input), SegmentFormatOf(input), One<VideoOptions>(input));
            case "query.imaginary_url":
                return Query(cfg).ImaginaryUrl(input.Str("endpoint")!, source, Params(input));
            case "sign":
                return Image.Signing.Sign(input.Str("message")!, Signing(cfg)!);
            case "sign_xi_url":
                return Image.Signing.SignXiUrl(input.Str("url")!, Signing(cfg)!, input.Str("mount") ?? string.Empty);
            case "encrypt_source":
                return Image.Encryption.EncryptSource(source, Encryption(cfg)!);
            case "encrypt_pdf_password":
                return Image.Encryption.EncryptPdfPassword(input.Str("password")!, Encryption(cfg)!);
            case "xi_source":
                return Sources.XiSource(input.Str("name")!, input.Str("key")!);
            case "source_url":
                return Sources.SourceUrl(input.Str("scheme")!, input.Str("authority")!, input.Str("key")!);
            case "range_header":
                return XtandardClient.RangeHeader(Range(input.GetProperty("range")));
            case "escape_plain_source":
                return Sources.EscapePlainSource(input.Str("value")!);
            case "escape_query_value":
                return Sources.EscapeQueryValue(input.Str("value")!);
            default:
                throw new InvalidOperationException($"unknown op {c.GetProperty("op")}");
        }
    }

    private static bool HasFluent(JsonElement c) =>
        c.GetProperty("op").GetString() is "imgproxy.url" or "imgproxy.info_url" or "imgproxy.video_url" or "query.url" or "query.video_url";

    /// <summary>The case through the fluent builders.</summary>
    private static string RunFluent(JsonElement c)
    {
        var cfg = c.Get("config");
        var input = c.GetProperty("input");
        var source = input.Str("source")!;
        var pipelines = input.Get("options") is { ValueKind: JsonValueKind.Array } o ? o.EnumerateArray().ToList() : [];
        var ext = input.Str("extension");
        var pdp = input.Str("pdf_password");
        var enc = Encoding(input);
        switch (c.GetProperty("op").GetString())
        {
            case "imgproxy.url":
                {
                    var chain = Imgproxy(cfg).Image(source);
                    for (var i = 0; i < pipelines.Count; i++)
                    {
                        if (i > 0)
                        {
                            chain.Pipeline();
                        }

                        Apply(chain, pipelines[i]);
                    }

                    if (enc is { } e)
                    {
                        chain.Encoding(e);
                    }

                    if (ext is not null)
                    {
                        chain.Extension(ext);
                    }

                    if (pdp is not null)
                    {
                        chain.WithPdfPassword(pdp);
                    }

                    return chain.Build();
                }

            case "imgproxy.info_url":
                {
                    var chain = Imgproxy(cfg).Info(source);
                    pipelines.ForEach(p => Apply(chain, p));
                    if (enc is { } e)
                    {
                        chain.Encoding(e);
                    }

                    if (pdp is not null)
                    {
                        chain.WithPdfPassword(pdp);
                    }

                    return chain.Build();
                }

            case "imgproxy.video_url":
                {
                    var chain = Imgproxy(cfg).Video(source);
                    pipelines.ForEach(p => Apply(chain, p));
                    if (enc is { } e)
                    {
                        chain.Encoding(e);
                    }

                    if (ext is not null)
                    {
                        chain.Extension(ext);
                    }

                    return chain.Build();
                }

            case "query.url":
                {
                    var chain = Query(cfg).Image(source);
                    foreach (var (k, v) in Params(input))
                    {
                        chain.Param(k, v);
                    }

                    for (var i = 0; i < pipelines.Count; i++)
                    {
                        if (i > 0)
                        {
                            chain.Pipeline();
                        }

                        Apply(chain, pipelines[i]);
                    }

                    if (ext is not null)
                    {
                        chain.Extension(ext);
                    }

                    if (pdp is not null)
                    {
                        chain.WithPdfPassword(pdp);
                    }

                    return chain.Build();
                }

            case "query.video_url":
                {
                    // Several pipelines are written in order into one /q/video URL.
                    var chain = Query(cfg).Video(source);
                    pipelines.ForEach(p => Apply(chain, p));
                    if (ext is not null)
                    {
                        chain.Extension(ext);
                    }

                    return chain.Build();
                }

            default:
                throw new InvalidOperationException("no fluent form");
        }
    }

    private static void Apply<TSelf>(OptionChain<TSelf> chain, JsonElement pipeline)
        where TSelf : OptionChain<TSelf>
    {
        foreach (var entry in pipeline.EnumerateArray())
        {
            chain.Option(entry[0].GetString()!, Fixtures.Args(entry[1]));
        }
    }
}
