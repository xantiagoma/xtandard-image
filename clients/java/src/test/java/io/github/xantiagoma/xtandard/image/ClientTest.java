package io.github.xantiagoma.xtandard.image;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.http.HttpResponse.BodyHandlers;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletionException;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;

/** The HTTP clients against a stand-in server (the JDK's com.sun.net.httpserver). */
class ClientTest {
  private static final SigningKey KEY = SigningKey.of("736563726574", "68656C6C6F");
  private static final byte[] BODY = new byte[1000];
  private static final Pattern RANGE = Pattern.compile("bytes=(\\d+)-(\\d*)");

  /** What the stand-in saw: method, path, raw query, headers and content of each request. */
  record Seen(
      String method, String path, String query, Map<String, List<String>> headers, String body) {
    String header(String name) {
      for (Map.Entry<String, List<String>> e : headers.entrySet()) {
        if (e.getKey().equalsIgnoreCase(name)) {
          return e.getValue().get(0);
        }
      }
      return null;
    }
  }

  private static final List<Seen> SEEN = new CopyOnWriteArrayList<>();
  private static HttpServer server;
  private static String base;
  private static String videoUrl;
  private static XtandardClient client;

  static {
    for (int i = 0; i < BODY.length; i++) {
      BODY[i] = (byte) i;
    }
  }

  @BeforeAll
  static void start() throws IOException {
    server = HttpServer.create(new InetSocketAddress(InetAddress.getLoopbackAddress(), 0), 0);
    server.createContext("/", ClientTest::handle);
    server.start();
    base = "http://127.0.0.1:" + server.getAddress().getPort();
    videoUrl =
        ImgproxyUrlBuilder.builder()
            .baseUrl(base)
            .signing(KEY)
            .build()
            .videoUrl("s3://m/clip.mov", VideoOptions.create().vd(2), "webm");
    client =
        XtandardClient.builder(base)
            .signing(KEY)
            .playgroundPath("/_admin/play/")
            .header("X-Test", "1")
            .timeout(Duration.ofSeconds(10))
            .build();
  }

  @AfterAll
  static void stop() {
    server.stop(0);
  }

  private static void send(
      HttpExchange ex, int status, String type, byte[] body, Map<String, String> h)
      throws IOException {
    if (type != null) {
      ex.getResponseHeaders().set("Content-Type", type);
    }
    h.forEach((k, v) -> ex.getResponseHeaders().set(k, v));
    boolean head = ex.getRequestMethod().equals("HEAD");
    ex.sendResponseHeaders(status, head || body.length == 0 ? -1 : body.length);
    if (!head && body.length > 0) {
      try (OutputStream o = ex.getResponseBody()) {
        o.write(body);
      }
    }
    ex.close();
  }

  private static void json(HttpExchange ex, int status, String json) throws IOException {
    send(ex, status, "application/json", json.getBytes(StandardCharsets.UTF_8), Map.of());
  }

  private static void text(HttpExchange ex, int status, String text) throws IOException {
    send(ex, status, "text/plain", text.getBytes(StandardCharsets.UTF_8), Map.of());
  }

  private static void handle(HttpExchange ex) throws IOException {
    String path = ex.getRequestURI().getRawPath();
    String query = ex.getRequestURI().getRawQuery();
    String content;
    try (InputStream in = ex.getRequestBody()) {
      content = new String(in.readAllBytes(), StandardCharsets.UTF_8);
    }
    SEEN.add(
        new Seen(ex.getRequestMethod(), path, query, Map.copyOf(ex.getRequestHeaders()), content));
    if (path.equals("/q") || path.startsWith("/q/")) {
      // The query dialect answers GET and QUERY alike (RFC 10008).
      send(ex, 200, "image/webp", "q".getBytes(StandardCharsets.UTF_8), Map.of());
    } else if (path.equals("/healthz")) {
      text(ex, 200, "ok\n");
    } else if (path.equals("/readyz")) {
      text(ex, 503, "draining\n");
    } else if (path.startsWith("/info/")) {
      json(ex, 200, "{\"width\":640,\"height\":480,\"format\":\"jpeg\"}");
    } else if (path.startsWith("/video/") && path.endsWith(".m3u8")) {
      send(
          ex,
          200,
          "application/vnd.apple.mpegurl",
          "#EXTM3U\n/_stream/T/v0.m3u8\n".getBytes(StandardCharsets.UTF_8),
          Map.of());
    } else if (path.startsWith("/video/") || path.startsWith("/_stream/")) {
      String full = base + path + (query == null ? "" : "?" + query);
      if (path.startsWith("/video/") && !full.equals(videoUrl)) {
        json(ex, 403, "{\"error\":{\"kind\":\"unauthorized\",\"message\":\"invalid signature\"}}");
        return;
      }
      String range = ex.getRequestHeaders().getFirst("Range");
      Matcher m = range == null ? null : RANGE.matcher(range);
      if (m != null && m.matches()) {
        int start = Integer.parseInt(m.group(1));
        int end = m.group(2).isEmpty() ? BODY.length - 1 : Integer.parseInt(m.group(2));
        send(
            ex,
            206,
            "video/webm",
            Arrays.copyOfRange(BODY, start, end + 1),
            Map.of("Content-Range", "bytes " + start + "-" + end + "/" + BODY.length));
        return;
      }
      send(ex, 200, "video/webm", BODY, Map.of());
    } else if (path.equals("/_admin/play/api/meta")) {
      json(ex, 200, "{\"version\":\"test\",\"n\":[1,2]}");
    } else if (path.equals("/proxy-error")) {
      send(
          ex,
          502,
          "text/html",
          "<html>bad gateway</html>".getBytes(StandardCharsets.UTF_8),
          Map.of());
    } else if (path.equals("/busy")) {
      json(
          ex,
          429,
          "{\"error\":{\"kind\":\"overloaded\",\"message\":\"too many requests\",\"request_id\":\"r1\"}}");
    } else if (path.equals("/bad-info/x")) {
      json(ex, 200, "{\"width\":\"wide\"}");
    } else {
      json(ex, 404, "{\"error\":{\"kind\":\"not_found\",\"message\":\"source image not found\"}}");
    }
  }

  private static Seen last() {
    return SEEN.get(SEEN.size() - 1);
  }

  @Test
  void health() {
    client.healthz();
    Readiness r = client.readyz();
    assertFalse(r.ready());
    assertEquals("draining", r.state());
  }

  @Test
  void info() {
    InfoResponse info = client.info("s3://b/cat.jpg", InfoOptions.create().size(true).format(true));
    assertEquals(640L, info.width());
    Seen s = last();
    assertEquals("application/json", s.header("Accept"));
    assertEquals("1", s.header("X-Test"));
    assertTrue(s.path().startsWith("/info/"));
    assertEquals("jpeg", client.infoAt(s.path()).format());
    TransportException e =
        assertThrows(TransportException.class, () -> client.infoAt("/bad-info/x"));
    assertInstanceOf(IllegalArgumentException.class, e.getCause());
  }

  @Test
  void videoWholeRangeAndHead() throws IOException {
    VideoOptions o = VideoOptions.create().vd(2);
    assertArrayEquals(BODY, client.video("s3://m/clip.mov", o, "webm").body());
    MediaResponse<byte[]> part =
        client.video(
            "s3://m/clip.mov",
            o,
            UrlParams.ofExtension("webm"),
            MediaRequest.get().withRange(ByteRange.of(10, 19)));
    assertEquals(206, part.status());
    assertEquals("bytes 10-19/1000", part.header("Content-Range").orElseThrow());
    assertArrayEquals(Arrays.copyOfRange(BODY, 10, 20), part.body());
    assertEquals("bytes=10-19", last().header("Range"));
    MediaResponse<byte[]> head =
        client.video("s3://m/clip.mov", o, UrlParams.ofExtension("webm"), MediaRequest.head());
    assertEquals("HEAD", last().method());
    assertEquals("video/webm", head.contentType().orElseThrow());
    assertEquals(0, head.body().length);
    MediaResponse<InputStream> streamed =
        client.media(videoUrl, MediaRequest.get(), BodyHandlers.ofInputStream());
    try (InputStream in = streamed.body()) {
      assertArrayEquals(BODY, in.readAllBytes());
    }
  }

  @Test
  void queryMethodOptIn() {
    String url =
        QueryUrlBuilder.builder()
            .signing(KEY)
            .build()
            .url("s3://b/cat.jpg", QueryParams.create().w(300));
    String query = url.substring(url.indexOf('?') + 1);

    // Off by default: a query URL goes out as GET.
    client.media(url);
    Seen s = last();
    assertEquals(
        List.of("GET", "/q/", query, ""), List.of(s.method(), s.path(), s.query(), s.body()));
    assertNull(s.header("Content-Type"));

    // One call as QUERY.
    MediaResponse<byte[]> res = client.media(url, MediaRequest.query().withAccept("image/webp"));
    assertEquals("q", new String(res.body(), StandardCharsets.UTF_8));
    s = last();
    assertEquals("QUERY", s.method());
    assertEquals("/q/", s.path());
    assertNull(s.query());
    assertEquals("application/x-www-form-urlencoded", s.header("Content-Type"));
    assertEquals("image/webp", s.header("Accept"));
    assertEquals("1", s.header("X-Test"));
    assertEquals(query, s.body());

    // queryMethod: URLs under the mount go out as QUERY unless the call names a method.
    XtandardClient on = XtandardClient.builder(base).queryMethod(true).build();
    on.media(url);
    assertEquals(List.of("QUERY", query), List.of(last().method(), last().body()));
    on.media(url, MediaRequest.head());
    assertEquals("HEAD", last().method());
    on.media(url, MediaRequest.get());
    assertEquals("GET", last().method());
    assertThrows(HttpException.class, () -> on.media("/busy"));
    assertEquals("GET", last().method());
    on.async().media(url).join();
    assertEquals("QUERY", last().method());

    // A custom mount.
    XtandardClient mounted =
        XtandardClient.builder(base).queryMethod(true).queryMount("/img/q/").build();
    assertThrows(
        HttpException.NotFound.class,
        () -> mounted.media("/img/q/watermark?url=s3://b/a.jpg&text=hi"));
    s = last();
    assertEquals(
        List.of("QUERY", "/img/q/watermark", "url=s3://b/a.jpg&text=hi"),
        List.of(s.method(), s.path(), s.body()));
    mounted.media(url);
    assertEquals("GET", last().method());

    // QUERY needs a URL with a query.
    UrlBuildException e =
        assertThrows(UrlBuildException.class, () -> client.media("/q/", MediaRequest.query()));
    assertEquals(ErrorCode.INVALID_REQUEST, e.code());
    assertThrows(
        IllegalArgumentException.class, () -> new MediaRequest("POST", null, "*/*", Map.of()));
  }

  @Test
  void badSignatureIsForbidden() {
    HttpException.Forbidden e =
        assertThrows(
            HttpException.Forbidden.class,
            () -> client.video("s3://m/clip.mov", VideoOptions.create().vd(3), "webm"));
    assertEquals(403, e.status());
    assertEquals("unauthorized", e.kind());
    assertEquals("invalid signature", e.getMessage());
  }

  @Test
  void streamAndTicketChild() {
    StreamManifest m =
        client.stream("s3://m/clip.mov", StreamParams.hls().withRenditions(360, 720));
    assertTrue(m.url().contains("/st:hls:360:720/"), m.url());
    assertEquals("application/vnd.apple.mpegurl", m.contentType());
    assertTrue(last().header("Accept").startsWith("application/vnd.apple.mpegurl"));
    String child = m.body().lines().skip(1).findFirst().orElseThrow();
    MediaResponse<byte[]> res =
        client.media(child, MediaRequest.get().withRange(ByteRange.suffix(4)));
    assertEquals("/_stream/T/v0.m3u8", last().path());
    assertEquals("bytes=-4", last().header("Range"));
    assertEquals(200, res.status()); // the stand-in only answers start-end ranges
  }

  @Test
  void errorsByStatus() {
    HttpException.NotFound nf =
        assertThrows(
            HttpException.NotFound.class,
            () -> client.image("s3://b/missing.jpg", ProcessingOptions.create()));
    assertEquals("not_found", nf.kind());
    HttpException.Gateway gw =
        assertThrows(HttpException.Gateway.class, () -> client.media("/proxy-error"));
    assertEquals("unknown", gw.kind());
    assertEquals("<html>bad gateway</html>", gw.getMessage());
    assertNull(gw.body());
    assertInstanceOf(HttpException.ServerError.class, gw);
    HttpException.TooManyRequests busy =
        assertThrows(HttpException.TooManyRequests.class, () -> client.media("/busy"));
    assertEquals("r1", busy.requestId());
    assertInstanceOf(HttpException.ServiceUnavailable.class, HttpException.of(503, "u", "m", null));
    assertInstanceOf(HttpException.BadRequest.class, HttpException.of(400, "u", "m", null));
    assertEquals(HttpException.class, HttpException.of(418, "u", "m", null).getClass());
  }

  @Test
  void transportError() throws IOException {
    int port;
    try (ServerSocket s = new ServerSocket(0, 1, InetAddress.getLoopbackAddress())) {
      port = s.getLocalPort();
    }
    XtandardClient down = XtandardClient.builder("http://127.0.0.1:" + port).build();
    assertThrows(TransportException.class, down::healthz);
    CompletionException e =
        assertThrows(CompletionException.class, () -> down.async().healthz().join());
    assertInstanceOf(TransportException.class, e.getCause());
  }

  @Test
  void playgroundMeta() {
    Map<String, Object> meta = client.playgroundMeta();
    assertEquals("test", meta.get("version"));
    assertEquals(List.of(1L, 2L), meta.get("n"));
  }

  @Test
  void urlBuildErrorsSurfaceSynchronouslyAndAsFailedFutures() {
    assertThrows(UrlBuildException.class, () -> client.image("", ProcessingOptions.create()));
    CompletionException e =
        assertThrows(
            CompletionException.class,
            () -> client.async().image("", ProcessingOptions.create()).join());
    assertInstanceOf(UrlBuildException.class, e.getCause());
  }

  @Test
  void asyncClient() throws IOException {
    XtandardAsyncClient xi = client.async();
    xi.healthz().join();
    assertEquals("draining", xi.readyz().join().state());
    assertEquals(480L, xi.info("s3://b/cat.jpg", InfoOptions.create().size(true)).join().height());
    MediaResponse<byte[]> part =
        xi.video(
                "s3://m/clip.mov",
                VideoOptions.create().vd(2),
                UrlParams.ofExtension("webm"),
                MediaRequest.get().withRange(ByteRange.of(0, 9)))
            .join();
    assertArrayEquals(Arrays.copyOf(BODY, 10), part.body());
    assertTrue(
        xi.stream("s3://m/clip.mov", StreamParams.hls()).join().body().startsWith("#EXTM3U"));
    CompletionException forbidden =
        assertThrows(
            CompletionException.class,
            () -> xi.video("s3://m/clip.mov", VideoOptions.create().vd(3), "webm").join());
    assertInstanceOf(HttpException.Forbidden.class, forbidden.getCause());
    assertEquals("test", xi.playgroundMeta().join().get("version"));
    try (InputStream in =
        xi.media(videoUrl, MediaRequest.get(), BodyHandlers.ofInputStream()).join().body()) {
      assertArrayEquals(BODY, in.readAllBytes());
    }
  }
}
