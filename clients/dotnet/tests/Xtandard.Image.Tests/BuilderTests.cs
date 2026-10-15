using System.Text.Json;
using static Xtandard.Image.ProcessingOption;

namespace Xtandard.Image.Tests;

/// <summary>The typed surface: generated records and per-option methods, number formatting, escapes, the tables.</summary>
public sealed class BuilderTests
{
    private const string Cat = "https://example.com/images/cat.jpg";
    private const string Cat64 = "aHR0cHM6Ly9leGFtcGxlLmNvbS9pbWFnZXMvY2F0LmpwZw";

    private static readonly ImgproxyUrlBuilder Img = new();

    [Fact]
    public void ObjectFormTypedRecordsAndFluentFormAgree()
    {
        var want = $"/insecure/rs:fill:300:400/q:80/bl:2/{Cat64}.webp";
        Assert.Equal(want, Img.Url(Cat, new() { { "rs", "fill", 300, 400 }, { "q", 80 }, { "blur", 2 } }, "webp"));
        Assert.Equal(want, Img.Url(Cat, new() { new Resize("fill", 300, 400), new Quality(80), new Blur(2) }, "webp"));
        Assert.Equal(want, Img.Image(Cat).Resize("fill", 300, 400).Quality(80).Blur(2).Extension("webp").Build());
        Assert.Equal(want, Img.Image(Cat).Rs("fill", 300, 400).Q(80).Bl(2).Extension("webp").ToString());
    }

    [Fact]
    public void FluentMethodsWriteTheNameAsCalledWithGivenStyle()
    {
        var given = new ImgproxyUrlBuilder(new() { OptionNames = OptionNameStyle.Given });
        Assert.Equal($"/insecure/resize:fit:10:10/w:5/{Cat64}", given.Image(Cat).Resize("fit", 10, 10).W(5).Build());
        var canonical = new ImgproxyUrlBuilder(new() { OptionNames = OptionNameStyle.Canonical });
        Assert.Equal($"/insecure/resize:fit:10:10/width:5/{Cat64}", canonical.Image(Cat).Rs("fit", 10, 10).W(5).Build());
    }

    [Fact]
    public void OptionalArgumentsAreTrimmedAndExplicitNullsAreEmpty()
    {
        Assert.Equal($"/insecure/rs:fill:300/{Cat64}", Img.Image(Cat).Resize("fill", 300).Build());
        Assert.Equal($"/insecure/rs:fill::400/{Cat64}", Img.Image(Cat).Resize("fill", null, 400).Build());
        Assert.Equal($"/insecure/rs:fill:300::1/{Cat64}", Img.Url(Cat, new() { { "rs", "fill", 300, OptionArg.Null, true } }));
        Assert.Equal($"/insecure/rs:fill::400/{Cat64}", Img.Url(Cat, new() { new Resize(type: "fill", height: 400) }));
    }

    [Fact]
    public void RestGroupsAndDirections()
    {
        Assert.Equal($"/insecure/c_obj:1.5:face:dog/{Cat64}", Img.Image(Cat).CropObjects(1.5, "face", "dog").Build());
        Assert.Equal($"/insecure/ba:2:0:0:0.5:0.5:0.5:0.5:0.25:0.25/{Cat64}", Img.Image(Cat).BlurAreas(2, (0, 0, 0.5, 0.5), (0.5, 0.5, 0.25, 0.25)).Build());
        Assert.Equal($"/insecure/ba:2:0:0:0.5:0.5/{Cat64}", Img.Url(Cat, new() { new BlurAreas(2, (0, 0, 0.5, 0.5)) }));
        Assert.Equal($"/insecure/gr:0.5:ff0000:down/{Cat64}", Img.Image(Cat).Gradient(0.5, "ff0000", "down").Build());
        Assert.Equal($"/insecure/gr:0.5:ff0000:45/{Cat64}", Img.Image(Cat).Gradient(0.5, "ff0000", 45).Build());
        Assert.Equal($"/insecure/g:fp:0.5:0.25/{Cat64}", Img.Image(Cat).Gravity("fp", 0.5, 0.25).Build());
        Assert.Equal($"/insecure/g:obj:face:cat/{Cat64}", Img.Image(Cat).Gravity("obj", "face", "cat").Build());
        Assert.Equal($"/insecure/ct:a:b/{Cat64}", Img.Image(Cat).CacheTags("a", "b").Build());
    }

    [Fact]
    public void RecordsCompareByValue()
    {
        Assert.Equal(new Resize("fill", 300, 400), new Resize("fill", 300, 400));
        Assert.NotEqual(new Resize("fill", 300, 400), new Resize("fit", 300, 400));
        Assert.NotEqual<OptionValue>(new ProcessingOption.Quality(80), new VideoOption.Quality(80));
        Assert.Equal("Resize { resize:fill:300:400 }", new Resize("fill", 300, 400).ToString());
    }

    [Fact]
    public void InfoVideoAndStreams()
    {
        Assert.Equal($"/info/insecure/s:1/f:1/{Cat64}", Img.Info(Cat).Size(true).Format(true).Build());
        Assert.Equal(
            "/video/insecure/rs:fill:640:360/vs:12/vd:8/czM6Ly9tZWRpYS90YWxrLm1vdg.webm",
            Img.VideoUrl("s3://media/talk.mov", new() { new VideoOption.Resize("fill", 640, 360), new VideoOption.Start(12), new VideoOption.Duration(8) }, "webm"));
        Assert.Equal(
            "/video/insecure/st:hls:360:720/sgd:4/czM6Ly9tZWRpYS90YWxrLm1vdg.m3u8",
            Img.StreamUrl("s3://media/talk.mov", StreamProtocol.Hls, [360, 720], segmentDuration: 4));
        var e = Assert.Throws<UrlBuildException>(() => Img.StreamUrl("s3://media/talk.mov", "dash", segmentFormat: SegmentFormat.Ts));
        Assert.Equal(UrlBuildErrorCode.InvalidRequest, e.Code);
    }

    [Fact]
    public void QueryDialect()
    {
        var q = new QueryUrlBuilder(new() { BaseUrl = "https://img.example.com" });
        Assert.Equal(
            "https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&lossless=true&o=bl:2",
            q.Url("s3://photos/cat.jpg", new() { { QueryKeys.W, 300 }, { QueryKeys.F, "webp" }, { "lossless", true }, { "skipped", OptionArg.Null } }, new() { new Blur(2) }));
        Assert.Equal(
            "https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&o=bl:2",
            q.Image("s3://photos/cat.jpg").Param("w", 300).Blur(2).Build());
        Assert.Equal(
            "https://img.example.com/q/resize?url=s3://photos/cat.jpg&width=300",
            q.ImaginaryUrl(ImaginaryEndpoints.Resize, "s3://photos/cat.jpg", new() { { "width", 300 } }));
        Assert.Equal(
            "https://img.example.com/q/video?url=s3://m/a.mov&c=100,100&fl=1,0&format=mp4",
            q.Video("s3://m/a.mov").Crop(100, 100).Flip(true, false).Extension("mp4").Build());
    }

    [Theory]
    [InlineData(300.0, "300")]
    [InlineData(0.1, "0.1")]
    [InlineData(1.5, "1.5")]
    [InlineData(-1.5, "-1.5")]
    [InlineData(1e-7, "1e-7")]
    [InlineData(1.5e-7, "1.5e-7")]
    [InlineData(0.000001, "0.000001")]
    [InlineData(1e21, "1e+21")]
    [InlineData(1.5e21, "1.5e+21")]
    [InlineData(1e20, "100000000000000000000")]
    [InlineData(123456789.125, "123456789.125")]
    [InlineData(0.30000000000000004, "0.30000000000000004")]
    [InlineData(-0.0, "0")]
    [InlineData(5e-324, "5e-324")]
    [InlineData(1.7976931348623157e308, "1.7976931348623157e+308")]
    public void NumbersAreWrittenLikeEcmaScript(double v, string want) => Assert.Equal(want, Text.FormatNumber(v, "test"));

    [Fact]
    public void NonFiniteNumbersAndOtherArgumentConversions()
    {
        foreach (var v in new[] { double.NaN, double.PositiveInfinity, double.NegativeInfinity })
        {
            var e = Assert.Throws<UrlBuildException>(() => Img.Url(Cat, new() { { "bl", v } }));
            Assert.Equal("invalid_argument", e.CodeName);
        }

        Assert.Equal("0.1", ((OptionArg)0.1f).ToString());
        Assert.Equal("2.5", ((OptionArg)2.5m).ToString());
        Assert.Equal("4102444800", ((OptionArg)4102444800L).ToString());
        Assert.Equal(string.Empty, OptionArg.Null.ToString());
        Assert.Equal("1", ((OptionArg)true).ToString());
    }

    [Fact]
    public void EscapesWorkPerUtf8Byte()
    {
        Assert.Equal("a%20b/%C3%BC%40x%3F%23", Sources.EscapePlainSource("a b/ü@x?#"));
        Assert.Equal("%F0%9F%98%80", Sources.EscapePlainSource("😀"));
        Assert.Equal("%EF%BF%BD", Sources.EscapePlainSource("\uD800"));
        Assert.Equal("https://e.com/a,b@c%26d%3De", Sources.EscapeQueryValue("https://e.com/a,b@c&d=e"));
    }

    [Fact]
    public void SplitSourceAndEncryptedSources()
    {
        var b = new ImgproxyUrlBuilder(new() { SplitSource = 16 });
        Assert.Equal("/insecure/aHR0cHM6Ly9leGFt/cGxlLmNvbS9pbWFn/ZXMvY2F0LmpwZw.png", b.Url(Cat, extension: "png"));
        var keyHex = new string('1', 64);
        var enc = new ImgproxyUrlBuilder(new() { Encryption = new EncryptionKey(keyHex), SourceEncoding = SourceEncoding.Encrypted });
        var url = enc.Url(Cat);
        Assert.StartsWith("/insecure/enc/", url, StringComparison.Ordinal);
        Assert.Equal(Cat, VectorTests.Decrypt(url["/insecure/enc/".Length..], keyHex));
    }

    [Fact]
    public void InvalidKeysAreRejectedAtCreation()
    {
        Assert.Equal(UrlBuildErrorCode.InvalidKey, Assert.Throws<UrlBuildException>(() => new SigningKey("zz", "00")).Code);
        Assert.Equal(UrlBuildErrorCode.InvalidKey, Assert.Throws<UrlBuildException>(() => new SigningKey("00", "00", 33)).Code);
        Assert.Equal(UrlBuildErrorCode.InvalidKey, Assert.Throws<UrlBuildException>(() => new EncryptionKey("0011")).Code);
        Assert.Equal(UrlBuildErrorCode.InvalidKey, Assert.Throws<UrlBuildException>(() => new EncryptionKey(new string('1', 32), "00")).Code);
        Assert.Equal(UrlBuildErrorCode.InvalidName, Assert.Throws<UrlBuildException>(() => Sources.XiSource("ok\n", "a")).Code);
    }

    [Fact]
    public void GeneratedTablesAreConsistent()
    {
        foreach (var (names, shorts) in new[]
        {
            (OptionCatalog.ProcessingNames, OptionCatalog.ProcessingShortNames),
            (OptionCatalog.InfoNames, OptionCatalog.InfoShortNames),
            (OptionCatalog.VideoNames, OptionCatalog.VideoShortNames),
        })
        {
            foreach (var canonical in names.Values.Distinct())
            {
                Assert.True(shorts.ContainsKey(canonical));
                Assert.Equal(canonical, names[shorts[canonical]]);
            }
        }

        Assert.Equal(90, OptionCatalog.ProcessingShortNames.Count);
        Assert.Equal(("m3u8", "application/vnd.apple.mpegurl"), XtandardSpec.StreamManifests["hls"]);
        Assert.Equal(400, XtandardSpec.ErrorStatus[ErrorKinds.InvalidUrl]);
    }

    [Fact]
    public void ResponsesDeserializeWithTheSourceGeneratedContext()
    {
        const string json = """
            {"width":640,"height":480,"format":"jpeg","size":1234,"exif":{"Make":"X"},"average":{"R":1,"G":2,"B":3,"A":255},
             "objects":[{"class_id":0,"class_name":"face","confidence":0.9,"left":0.1,"top":0.2,"width":0.3,"height":0.4,"landmarks":[[0.1,0.2],null]}],
             "video_streams":[{"type":"video","codec":"h264","fps":29.97}]}
            """;
        var info = JsonSerializer.Deserialize(json, XtandardJsonContext.Default.InfoResponse)!;
        Assert.Equal(640, info.Width);
        Assert.Equal("jpeg", info.Format);
        Assert.Equal("X", info.Exif!["Make"]);
        Assert.Equal(255, info.Average!.A);
        Assert.Equal("face", info.Objects![0].ClassName);
        Assert.Null(info.Objects[0].Landmarks![1]);
        Assert.Equal(29.97, info.VideoStreams![0].Fps);
        Assert.Null(info.Iptc);
    }
}
