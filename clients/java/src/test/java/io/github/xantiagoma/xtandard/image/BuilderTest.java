package io.github.xantiagoma.xtandard.image;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.math.BigInteger;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.junit.jupiter.params.provider.ValueSource;

/** Unit tests beyond the shared fixtures: Java-specific forms, numbers, JSON. */
class BuilderTest {
  private static final String SRC = "https://example.com/a.jpg";

  @ParameterizedTest
  @CsvSource({
    "0, 0",
    "-0.0, 0",
    "300, 300",
    "300.0, 300",
    "1.5, 1.5",
    "-1.5, -1.5",
    "0.1, 0.1",
    "0.000001, 0.000001",
    "1e-7, 1e-7",
    "1.5e-9, 1.5e-9",
    "123456789.125, 123456789.125",
    "1e20, 100000000000000000000",
    "1e21, 1e+21",
    "1.5e22, 1.5e+22",
    "0.30000000000000004, 0.30000000000000004",
    // Double.toString on JDK 17 writes these with too many digits (JDK-4511638).
    "2.82879384806159E17, 282879384806159000",
    "2.0E-3, 0.002",
    "1.0E23, 1e+23",
    "4.9E-324, 5e-324",
    "1.7976931348623157E308, 1.7976931348623157e+308",
  })
  void numbersAreWrittenAsEcmaScriptWritesThem(double value, String want) {
    assertEquals(want, Text.formatDouble(value, "x"));
  }

  @Test
  void integralNumbersAreExact() {
    assertEquals("9007199254740993", Text.formatNumber(9007199254740993L, "x"));
    assertEquals("-7", Text.formatNumber((short) -7, "x"));
    assertEquals("1.1", Text.formatNumber(1.1f, "x"));
    assertEquals("1e+21", Text.formatNumber(BigInteger.TEN.pow(21), "x"));
    assertEquals(
        "999999999999999999999",
        Text.formatNumber(BigInteger.TEN.pow(21).subtract(BigInteger.ONE), "x"));
  }

  @ParameterizedTest
  @ValueSource(doubles = {Double.POSITIVE_INFINITY, Double.NEGATIVE_INFINITY, Double.NaN})
  void nonFiniteNumbersAreRefused(double v) {
    UrlBuildException e = assertThrows(UrlBuildException.class, () -> Text.formatDouble(v, "x"));
    assertEquals(ErrorCode.INVALID_ARGUMENT, e.code());
  }

  @Test
  void typedMethodsBooleansAndNulls() {
    ImgproxyUrlBuilder img = ImgproxyUrlBuilder.create();
    ProcessingOptions o =
        ProcessingOptions.create().rs("fill", 300, 400).q(80).g("sm").adjust(null, 1.1, null);
    assertEquals(
        "/insecure/rs:fill:300:400/q:80/g:sm/a::1.1:/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw",
        img.url(SRC, o));
    assertEquals(
        "/insecure/el:1/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw",
        img.url(SRC, ProcessingOptions.create().enlarge(true)));
  }

  @Test
  void chainedOptionLists() {
    ImgproxyUrlBuilder img =
        ImgproxyUrlBuilder.builder().sourceEncoding(SourceEncoding.PLAIN).build();
    assertEquals(
        "/insecure/rs:fit:100:100/-/bl:2/plain/https://example.com/a.jpg",
        img.url(
            SRC,
            List.of(
                ProcessingOptions.create().resize("fit", 100, 100),
                ProcessingOptions.create().blur(2)),
            UrlParams.of()));
  }

  @Test
  void fluentMethodsUseTheNameCalled() {
    ImgproxyUrlBuilder b =
        ImgproxyUrlBuilder.builder()
            .optionNames(OptionNameStyle.GIVEN)
            .sourceEncoding(SourceEncoding.PLAIN)
            .build();
    assertEquals(
        "/insecure/resize:fit:1:2/q:90/min-width:10/plain/https://example.com/a.jpg",
        b.image(SRC).resize("fit", 1, 2).q(90).minWidth(10).build());
    assertEquals(b.url(SRC, ProcessingOptions.create().w(100)), b.image(SRC).w(100).toString());
    assertEquals(
        b.image(SRC)
            .options(ProcessingOptions.create().w(1).h(2))
            .extension(ImageFormat.WEBP)
            .build(),
        b.url(SRC, ProcessingOptions.create().w(1).h(2), "webp"));
  }

  @Test
  void videoAndStreams() {
    ImgproxyUrlBuilder b =
        ImgproxyUrlBuilder.builder().sourceEncoding(SourceEncoding.PLAIN).build();
    assertEquals(
        "/video/insecure/rs:fill:640:360/vs:1.5/vd:5/plain/s3://m/a.mov@webm",
        b.videoUrl(
            "s3://m/a.mov",
            VideoOptions.create().rs("fill", 640, 360).vs(1.5).vd(5),
            VideoContainer.WEBM));
    assertEquals(
        "/video/insecure/vs:2/mu:1/plain/s3://m/a.mov@mp4",
        b.video("s3://m/a.mov").start(2).mute(true).extension("mp4").build());
    assertEquals(
        "/video/insecure/st:dash:360/plain/s3://m/a.mov@mpd",
        b.streamUrl("s3://m/a.mov", StreamParams.dash().withRenditions(360)));
    assertEquals(
        "/video/insecure/st:hls:240:480/sgd:4/sgf:ts/plain/s3://m/a.mov@m3u8",
        b.streamUrl(
            "s3://m/a.mov",
            StreamParams.hls()
                .withRenditions(240, 480)
                .withSegmentDuration(4)
                .withSegmentFormat(SegmentFormat.TS)));
    assertEquals(
        "/video/insecure/st:hls:360/plain/s3://m/a.mov@m3u8",
        b.streamUrl(
            "s3://m/a.mov", StreamParams.of(StreamProtocol.HLS).withRenditions(List.of(360))));
  }

  @Test
  void infoTakesNoExtension() {
    UrlBuildException e =
        assertThrows(
            UrlBuildException.class,
            () ->
                ImgproxyUrlBuilder.create()
                    .infoUrl(SRC, InfoOptions.create().size(true), UrlParams.ofExtension("png")));
    assertEquals(ErrorCode.INVALID_REQUEST, e.code());
  }

  @Test
  void queryDialect() {
    QueryUrlBuilder q = QueryUrlBuilder.create();
    assertEquals(
        "/q/?url=https://example.com/a.jpg&w=300&enlarge=true",
        q.url(SRC, QueryParams.create().w(300).enlarge(true)));
    assertEquals(
        "/q/?url=https://example.com/a.jpg&w=300&o=bl:2",
        q.image(SRC).param("w", 300).bl(2).build());
    assertEquals(
        "/q/resize?url=https://example.com/a.jpg&width=10",
        q.imaginaryUrl(ImaginaryEndpoint.RESIZE, SRC, ImaginaryParams.create().width(10)));
    assertEquals(
        "/q/video?url=s3://m/a.mov&c=10,20&fl=1,0&format=webm",
        QueryUrlBuilder.builder()
            .optionNames(OptionNameStyle.CANONICAL)
            .build()
            .videoUrl(
                "s3://m/a.mov", VideoOptions.create().crop(10, 20).flip(true, false), "webm"));
    UrlBuildException e =
        assertThrows(
            UrlBuildException.class,
            () ->
                q.url(
                    SRC, null, List.of(), UrlParams.of().withSourceEncoding(SourceEncoding.PLAIN)));
    assertEquals(ErrorCode.INVALID_REQUEST, e.code());
  }

  @Test
  void unsupportedArgumentType() {
    UrlBuildException e =
        assertThrows(
            UrlBuildException.class,
            () -> ImgproxyUrlBuilder.create().image(SRC).option("w", new Object()).build());
    assertEquals(ErrorCode.INVALID_ARGUMENT, e.code());
    UrlBuildException u =
        assertThrows(
            UrlBuildException.class,
            () -> ImgproxyUrlBuilder.create().image(SRC).option("nope", 1).build());
    assertEquals(ErrorCode.UNKNOWN_OPTION, u.code());
  }

  @Test
  void nullVarargsIsOneEmptyArgument() {
    assertEquals(
        "/insecure/a:/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw",
        ImgproxyUrlBuilder.create().image(SRC).option("a", (Object[]) null).build());
  }

  @Test
  void rangeValidation() {
    assertEquals("bytes=0-", ByteRange.from(0).header());
    assertEquals("bytes=5-9", ByteRange.of(5, 9).header());
    assertEquals("bytes=-100", ByteRange.suffix(100).header());
    assertEquals("bytes=1-2", XtandardClient.rangeHeader(ByteRange.raw("bytes=1-2")));
    assertThrows(UrlBuildException.class, () -> ByteRange.of(5, 4));
    assertThrows(UrlBuildException.class, () -> ByteRange.from(-1));
    assertThrows(UrlBuildException.class, () -> ByteRange.suffix(-1));
  }

  @Test
  void jsonParser() {
    Object v =
        Json.parse(" {\"a\": [1, -2.5e3, true, false, null, \"\\u00e9\\n\\\"\"], \"b\": {}} ");
    Map<String, Object> m = Fixtures.obj(v);
    assertEquals(List.of(1L, -2500.0, true, false), Fixtures.list(m.get("a")).subList(0, 4));
    assertNull(Fixtures.list(m.get("a")).get(4));
    assertEquals("é\n\"", Fixtures.list(m.get("a")).get(5));
    assertEquals(Map.of(), m.get("b"));
    assertEquals(
        new BigInteger("123456789012345678901234567890"),
        Json.parse("123456789012345678901234567890"));
    for (String bad :
        List.of("", "{", "[1,]", "01", "1.", "\"a", "tru", "{\"a\" 1}", "[] x", "\"\t\"")) {
      assertThrows(IllegalArgumentException.class, () -> Json.parse(bad), bad);
    }
  }

  @Test
  void infoResponseDecodes() {
    InfoResponse r =
        InfoResponse.fromJson(
            "{\"width\":640,\"height\":480,\"format\":\"jpeg\",\"alpha\":false,"
                + "\"average\":{\"R\":1,\"G\":2,\"B\":3,\"A\":255},"
                + "\"exif\":{\"Make\":\"X\"},\"palette\":null,"
                + "\"objects\":[{\"class_id\":0,\"class_name\":\"face\",\"confidence\":0.9,"
                + "\"left\":0.1,\"top\":0.2,\"width\":0.3,\"height\":0.4,\"landmarks\":[[0.1,0.2]]}],"
                + "\"video_meta\":{\"k\":[1,{\"x\":null}]},\"future_field\":1}");
    assertEquals(640L, r.width());
    assertEquals("jpeg", r.format());
    assertEquals(false, r.alpha());
    assertEquals(255, r.average().a());
    assertEquals(Map.of("Make", "X"), r.exif());
    assertNull(r.palette());
    assertNull(r.blurhash());
    InfoObject face = r.objects().get(0);
    assertEquals("face", face.className());
    assertEquals(List.of(List.of(0.1, 0.2)), face.landmarks());
    assertTrue(r.videoMeta().containsKey("k"));
    assertThrows(IllegalArgumentException.class, () -> InfoResponse.fromJson("{\"width\":\"x\"}"));
    ErrorResponse e =
        ErrorResponse.fromJson(
            "{\"error\":{\"kind\":\"not_found\",\"message\":\"m\",\"request_id\":\"r\"}}");
    assertEquals("not_found", e.error().kind());
    assertEquals("r", e.error().requestId());
  }

  @Test
  void facade() {
    assertEquals("xi://photos/a.jpg", Xtandard.xiSource("photos", "a.jpg"));
    assertEquals(
        "https://i.example.com/insecure/w:1/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw",
        Xtandard.imgproxy().baseUrl("https://i.example.com/").build().image(SRC).w(1).build());
    assertEquals("/q/?url=x", Xtandard.queryUrls().build().url("x", QueryParams.create()));
  }

  @Test
  void queryMethodRequestSplitsAtTheFirstQuestionMark() {
    assertEquals(
        new QueryMethodRequest(
            "QUERY",
            "https://img.example.com/q/",
            QueryMethodRequest.CONTENT_TYPE,
            "url=s3://b/a.jpg?x=1&w=3"),
        Xtandard.queryMethodRequest("https://img.example.com/q/?url=s3://b/a.jpg?x=1&w=3"));
    assertEquals("application/x-www-form-urlencoded", QueryMethodRequest.CONTENT_TYPE);
  }

  @ParameterizedTest
  @ValueSource(strings = {"/q/", "/q/?", "/q/?url=a#f"})
  void queryMethodRequestNeedsAQueryAndNoFragment(String url) {
    UrlBuildException e = assertThrows(UrlBuildException.class, () -> QueryMethodRequest.from(url));
    assertEquals(ErrorCode.INVALID_REQUEST, e.code());
  }
}
