using System.Text.Json;

namespace Xtandard.Image.Tests;

/// <summary>
/// testdata/urls/imgproxy-builder.json: 400+ cases derived from the server's golden tables. Every client
/// must write the same URLs: <c>url</c> with option names as given, <c>url_short</c> with the default short
/// names, and <c>query_url</c> (query dialect) where the case has one.
/// </summary>
public sealed class BuilderFixtureTests
{
    private static JsonElement Fixture => Fixtures.Builder.Value;

    public static TheoryData<int, string> Cases()
    {
        var data = new TheoryData<int, string>();
        var i = 0;
        foreach (var c in Fixture.GetProperty("cases").EnumerateArray())
        {
            data.Add(i++, c.GetProperty("name").GetString()!);
        }

        return data;
    }

    [Fact]
    public void CoversTheTables() => Assert.True(Fixture.GetProperty("cases").GetArrayLength() >= 400);

    [Theory]
    [MemberData(nameof(Cases))]
    public void Case(int index, string name)
    {
        _ = name;
        var c = Fixture.GetProperty("cases")[index];
        Assert.Equal(c.GetProperty("url").GetString(), Fluent(c, OptionNameStyle.Given));
        Assert.Equal(c.GetProperty("url_short").GetString(), Fluent(c, OptionNameStyle.Short));
        if (c.Get("query").ValueKind == JsonValueKind.True)
        {
            Assert.Equal(c.GetProperty("query_url").GetString(), QueryUrl(c));
        }
    }

    private static SigningKey? Signing(JsonElement c)
    {
        var size = c.Has("signature_size") ? c.GetProperty("signature_size").GetInt32() : 0;
        var k = Fixture.GetProperty("signing");
        return size == 0 ? null : new SigningKey(k.Str("key")!, k.Str("salt")!, size);
    }

    private static string Fluent(JsonElement c, OptionNameStyle style)
    {
        var e = Fixture.GetProperty("encryption");
        var b = new ImgproxyUrlBuilder(new()
        {
            UnsignedSignature = "unsafe",
            Encryption = new EncryptionKey(e.Str("key")!, e.Str("iv")),
            Signing = Signing(c),
            Separator = c.Str("separator") ?? ":",
            OptionNames = style,
        });
        var source = c.Str("source")!;
        var encoding = Fixtures.Encoding(c.Str("encoding")!);
        var ext = c.Str("extension");
        var pipelines = c.GetProperty("pipelines").EnumerateArray().ToList();
        switch (c.Str("endpoint"))
        {
            case "info":
                {
                    var chain = b.Info(source).Encoding(encoding);
                    pipelines.ForEach(p => Apply(chain, p));
                    return chain.Build();
                }

            case "video":
                {
                    var chain = b.Video(source).Encoding(encoding);
                    pipelines.ForEach(p => Apply(chain, p));
                    if (!string.IsNullOrEmpty(ext))
                    {
                        chain.Extension(ext);
                    }

                    return chain.Build();
                }

            default:
                {
                    var chain = b.Image(source).Encoding(encoding);
                    for (var i = 0; i < pipelines.Count; i++)
                    {
                        if (i > 0)
                        {
                            chain.Pipeline();
                        }

                        Apply(chain, pipelines[i]);
                    }

                    if (!string.IsNullOrEmpty(ext))
                    {
                        chain.Extension(ext);
                    }

                    return chain.Build();
                }
        }
    }

    private static string QueryUrl(JsonElement c)
    {
        var q = new QueryUrlBuilder(new() { Signing = Signing(c) });
        var source = c.Str("source")!;
        var ext = c.Str("extension");
        var pipelines = c.GetProperty("pipelines").EnumerateArray().ToList();
        if (c.Str("endpoint") == "video")
        {
            var chain = q.Video(source);
            pipelines.ForEach(p => Apply(chain, p));
            if (!string.IsNullOrEmpty(ext))
            {
                chain.Extension(ext);
            }

            return chain.Build();
        }

        var image = q.Image(source);
        for (var i = 0; i < pipelines.Count; i++)
        {
            if (i > 0)
            {
                image.Pipeline();
            }

            Apply(image, pipelines[i]);
        }

        if (!string.IsNullOrEmpty(ext))
        {
            image.Extension(ext);
        }

        return image.Build();
    }

    private static void Apply<TSelf>(OptionChain<TSelf> chain, JsonElement pipeline)
        where TSelf : OptionChain<TSelf>
    {
        foreach (var entry in pipeline.EnumerateArray())
        {
            foreach (var option in entry.EnumerateObject())
            {
                chain.Option(option.Name, Fixtures.Args(option.Value));
            }
        }
    }
}
