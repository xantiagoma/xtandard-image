package io.github.xantiagoma.xtandard.image;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.net.http.HttpResponse.BodyHandler;
import java.net.http.HttpResponse.BodySubscribers;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.regex.Pattern;
import org.jspecify.annotations.Nullable;

/** What the sync and async clients share: URLs, requests and response checks. */
final class ClientCore {
  private static final Pattern SCHEME = Pattern.compile("^[a-zA-Z][a-zA-Z0-9+.-]*:");

  final HttpClient http;
  final ImgproxyUrlBuilder urls;
  final String baseUrl;
  final Map<String, String> headers;
  final Duration timeout;
  final String playgroundMeta;

  ClientCore(
      HttpClient http,
      ImgproxyUrlBuilder urls,
      Map<String, String> headers,
      Duration timeout,
      String playgroundMeta) {
    this.http = http;
    this.urls = urls;
    this.baseUrl = urls.baseUrl();
    this.headers = Map.copyOf(headers);
    this.timeout = timeout;
    this.playgroundMeta = playgroundMeta;
  }

  /** An absolute URL for a URL or a server path. */
  String abs(String url) {
    if (SCHEME.matcher(url).lookingAt()) {
      return url;
    }
    return baseUrl + (url.startsWith("/") ? "" : "/") + url;
  }

  HttpRequest request(String url, MediaRequest m) {
    HttpRequest.Builder b;
    try {
      b = HttpRequest.newBuilder(URI.create(url));
    } catch (IllegalArgumentException e) {
      throw new TransportException("invalid request URL " + url, e);
    }
    b.timeout(timeout);
    b.method(m.method(), HttpRequest.BodyPublishers.noBody());
    Map<String, String> h = new LinkedHashMap<>();
    h.put("User-Agent", "xtandard-image-java");
    h.putAll(headers);
    h.putAll(m.headers());
    h.put("Accept", m.accept());
    ByteRange r = m.range();
    if (r != null) {
      h.put("Range", r.header());
    }
    h.forEach(b::setHeader);
    return b.build();
  }

  /** A body that is the delegate's for a 2xx response and the raw bytes otherwise. */
  static final class Checked<T> {
    final @Nullable T ok;
    final byte @Nullable [] error;

    private Checked(@Nullable T ok, byte @Nullable [] error) {
      this.ok = ok;
      this.error = error;
    }
  }

  static <T> BodyHandler<Checked<T>> checked(BodyHandler<T> delegate) {
    return info -> {
      if (info.statusCode() / 100 == 2) {
        return BodySubscribers.mapping(delegate.apply(info), v -> new Checked<T>(v, null));
      }
      return BodySubscribers.mapping(BodySubscribers.ofByteArray(), b -> new Checked<T>(null, b));
    };
  }

  /**
   * The 2xx response, or the {@link HttpException} of the status. A 2xx body is the delegate's,
   * which is null only for a discarding handler.
   */
  @SuppressWarnings("NullAway")
  static <T> MediaResponse<T> unwrap(HttpResponse<Checked<T>> res) {
    Checked<T> c = res.body();
    String url = res.request().uri().toString();
    byte[] error = c.error;
    if (error != null) {
      throw toHttpError(res.statusCode(), url, new String(error, StandardCharsets.UTF_8));
    }
    return new MediaResponse<>(res.statusCode(), url, res.headers(), c.ok);
  }

  static HttpException toHttpError(int status, String url, String text) {
    ErrorResponse body = envelope(text);
    String message;
    if (body != null) {
      message = body.error().message();
    } else {
      String t = text.strip();
      message = t.isEmpty() ? "HTTP " + status : t;
    }
    return HttpException.of(status, url, message, body);
  }

  static @Nullable ErrorResponse envelope(String text) {
    try {
      return ErrorResponse.fromJson(text);
    } catch (IllegalArgumentException e) {
      return null;
    }
  }

  static Readiness readiness(int status, String url, String text) {
    if (status != 200 && status != 503) {
      throw toHttpError(status, url, text);
    }
    String state = text.strip();
    if (!state.equals("ready") && !state.equals("starting") && !state.equals("draining")) {
      throw new HttpException(status, url, "unexpected readiness state \"" + state + "\"", null);
    }
    return new Readiness(status == 200 && state.equals("ready"), state);
  }

  static String manifestType(String protocol) {
    for (StreamProtocol p : StreamProtocol.values()) {
      if (p.value().equals(protocol)) {
        return p.contentType();
      }
    }
    return "application/octet-stream";
  }

  static String text(byte[] b) {
    return new String(b, StandardCharsets.UTF_8);
  }

  static InfoResponse info(MediaResponse<byte[]> res) {
    try {
      return InfoResponse.fromJson(text(res.body()));
    } catch (IllegalArgumentException e) {
      throw new TransportException("invalid /info response from " + res.url(), e);
    }
  }

  static Map<String, @Nullable Object> jsonObject(MediaResponse<byte[]> res) {
    try {
      return JsonValues.object(Json.parse(text(res.body())), "response");
    } catch (IllegalArgumentException e) {
      throw new TransportException("invalid JSON response from " + res.url(), e);
    }
  }

  static String playgroundMeta(String origin, String path) {
    String p = path;
    while (p.startsWith("/")) {
      p = p.substring(1);
    }
    return Signing.stripTrailingSlashes(origin)
        + "/"
        + Signing.stripTrailingSlashes(p)
        + "/api/meta";
  }
}
