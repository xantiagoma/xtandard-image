package io.github.xantiagoma.xtandard.image;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;

/**
 * Smoke test against a running server, off unless XI_SMOKE_URL is set. The playground demo
 * (compose.playground.yml, unsigned URLs allowed) works: {@code XI_SMOKE_URL=http://127.0.0.1:8080
 * task test}. It needs the demo's local fixtures (xi://local/...) and, for the /video cases, a full
 * or gpl image with XI_VIDEO_TRANSFORM_ENABLED.
 */
@EnabledIfEnvironmentVariable(named = "XI_SMOKE_URL", matches = ".+")
class SmokeTest {
  private static final String BASE = System.getenv("XI_SMOKE_URL");
  private static final String IMAGE = Sources.xiSource("local", "jpeg/baseline.jpg");
  private static final String VIDEO = Sources.xiSource("local", "video/bbb-h264-aac.mp4");

  @Test
  void healthAndImage() {
    XtandardClient xi = XtandardClient.builder(BASE).sourceEncoding(SourceEncoding.PLAIN).build();
    xi.healthz();
    assertTrue(xi.readyz().ready());
    MediaResponse<byte[]> res =
        xi.image(
            IMAGE, ProcessingOptions.create().rs("fit", 64, 64), UrlParams.ofExtension("webp"));
    assertEquals("image/webp", res.contentType().orElseThrow());
    InfoResponse info =
        xi.info(IMAGE, InfoOptions.create().size(true).format(true).dimensions(true));
    assertEquals("jpeg", info.format());
    assertTrue(info.width() != null && info.width() > 0);
    assertThrows(
        HttpException.NotFound.class,
        () -> xi.image(Sources.xiSource("local", "jpeg/missing.jpg"), ProcessingOptions.create()));
  }

  @Test
  void queryDialect() {
    String url =
        QueryUrlBuilder.builder()
            .baseUrl(BASE)
            .build()
            .url(IMAGE, QueryParams.create().w(32).f("png"));
    XtandardClient xi = XtandardClient.builder(BASE).build();
    assertEquals("image/png", xi.media(url).contentType().orElseThrow());
  }

  @Test
  void videoRangeAndStream() {
    XtandardClient xi = XtandardClient.builder(BASE).build();
    VideoOptions o = VideoOptions.create().vd(1).w(160);
    MediaResponse<byte[]> part =
        xi.video(
            VIDEO,
            o,
            UrlParams.ofExtension("webm"),
            MediaRequest.get().withRange(ByteRange.of(0, 99)));
    assertTrue(part.status() == 200 || part.status() == 206, "status " + part.status());
    MediaResponse<byte[]> head =
        xi.video(VIDEO, o, UrlParams.ofExtension("webm"), MediaRequest.head());
    assertTrue(head.contentType().orElseThrow().startsWith("video/webm"));
    StreamManifest m = xi.stream(VIDEO, StreamParams.hls().withRenditions(240));
    assertTrue(m.body().startsWith("#EXTM3U"));
    String child =
        m.body().lines().filter(l -> !l.isEmpty() && !l.startsWith("#")).findFirst().orElseThrow();
    assertEquals(200, xi.media(child).status());
  }

  @Test
  void async() {
    XtandardAsyncClient xi = XtandardClient.builder(BASE).build().async();
    xi.healthz().join();
    InfoResponse info = xi.info(IMAGE, InfoOptions.create().size(true)).join();
    assertTrue(info.size() != null && info.size() > 0);
  }
}
