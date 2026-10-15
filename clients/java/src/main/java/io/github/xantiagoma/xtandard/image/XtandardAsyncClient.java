package io.github.xantiagoma.xtandard.image;

import java.io.IOException;
import java.net.http.HttpResponse;
import java.net.http.HttpResponse.BodyHandler;
import java.net.http.HttpResponse.BodyHandlers;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionException;
import java.util.function.Supplier;
import org.jspecify.annotations.Nullable;

/**
 * The asynchronous view of an {@link XtandardClient} ({@link XtandardClient#async()}): the same
 * calls, returning {@link CompletableFuture}s on {@link java.net.http.HttpClient#sendAsync}.
 *
 * <p>A future fails with the exception the synchronous call would throw: {@link UrlBuildException},
 * {@link HttpException} or {@link TransportException} ({@code join()} wraps it in a {@link
 * CompletionException}, {@code get()} in an {@link java.util.concurrent.ExecutionException}).
 * Thread-safe.
 *
 * <pre>{@code
 * xi.async().info("s3://bucket/cat.jpg", InfoOptions.create().size(true))
 *     .thenAccept(info -> System.out.println(info.width()));
 * }</pre>
 */
public final class XtandardAsyncClient {
  private final ClientCore core;

  XtandardAsyncClient(ClientCore core) {
    this.core = core;
  }

  private static <T> CompletableFuture<T> start(Supplier<CompletableFuture<T>> f) {
    try {
      return f.get();
    } catch (XtandardException e) {
      return CompletableFuture.failedFuture(e);
    }
  }

  private static RuntimeException transport(String what, Throwable ex) {
    Throwable cause =
        ex instanceof CompletionException && ex.getCause() != null ? ex.getCause() : ex;
    if (cause instanceof XtandardException x) {
      return x;
    }
    if (cause instanceof IOException) {
      return new TransportException(what + ": " + cause, cause);
    }
    return cause instanceof RuntimeException r
        ? r
        : new TransportException(what + ": " + cause, cause);
  }

  private <T> CompletableFuture<MediaResponse<T>> send(
      String url, MediaRequest m, BodyHandler<T> handler) {
    return start(
        () ->
            core.http
                .sendAsync(core.request(url, m), ClientCore.checked(handler))
                .handle(
                    (res, ex) -> {
                      if (ex != null) {
                        throw transport(m.method() + " " + url, ex);
                      }
                      return ClientCore.unwrap(res);
                    }));
  }

  /**
   * {@code GET /healthz}.
   *
   * @return a future that completes while the process can serve HTTP
   */
  public CompletableFuture<Void> healthz() {
    return send(
            core.baseUrl + "/healthz",
            MediaRequest.get().withAccept("text/plain"),
            BodyHandlers.discarding())
        .thenApply(r -> null);
  }

  /**
   * {@code GET /readyz}; 503 (starting, draining) is not an error.
   *
   * @return the state
   */
  public CompletableFuture<Readiness> readyz() {
    String url = core.baseUrl + "/readyz";
    return start(
        () ->
            core.http
                .sendAsync(
                    core.request(url, MediaRequest.get().withAccept("text/plain")),
                    BodyHandlers.ofByteArray())
                .handle(
                    (HttpResponse<byte[]> res, @Nullable Throwable ex) -> {
                      if (ex != null) {
                        throw transport("GET " + url, ex);
                      }
                      return ClientCore.readiness(
                          res.statusCode(), url, ClientCore.text(res.body()));
                    }));
  }

  /**
   * {@code GET /info} for a source without options.
   *
   * @param source the source URL
   * @return the decoded answer
   */
  public CompletableFuture<InfoResponse> info(String source) {
    return info(source, InfoOptions.create(), UrlParams.of());
  }

  /**
   * {@code GET /info} for a source.
   *
   * @param source the source URL
   * @param options the {@code /info} options
   * @return the decoded answer
   */
  public CompletableFuture<InfoResponse> info(String source, InfoOptions options) {
    return info(source, options, UrlParams.of());
  }

  /**
   * {@code GET /info} with per-URL settings.
   *
   * @param source the source URL
   * @param options the {@code /info} options
   * @param params the source encoding and PDF password
   * @return the decoded answer
   */
  public CompletableFuture<InfoResponse> info(
      String source, InfoOptions options, UrlParams params) {
    return start(() -> infoAt(core.urls.infoUrl(source, options, params)));
  }

  /**
   * {@code GET} a prebuilt {@code /info/...} path or URL.
   *
   * @param url the path or URL
   * @return the decoded answer
   */
  public CompletableFuture<InfoResponse> infoAt(String url) {
    return send(
            core.abs(url),
            MediaRequest.get().withAccept("application/json"),
            BodyHandlers.ofByteArray())
        .thenApply(ClientCore::info);
  }

  /**
   * {@code GET} any URL or server path.
   *
   * @param url the path or URL
   * @return the response
   */
  public CompletableFuture<MediaResponse<byte[]>> media(String url) {
    return media(url, MediaRequest.get());
  }

  /**
   * {@code GET} or {@code HEAD} any URL or server path.
   *
   * @param url the path or URL
   * @param request the method, range, Accept and headers
   * @return the response
   */
  public CompletableFuture<MediaResponse<byte[]>> media(String url, MediaRequest request) {
    return media(url, request, BodyHandlers.ofByteArray());
  }

  /**
   * {@code GET} or {@code HEAD} any URL or server path, with a body handler.
   *
   * @param url the path or URL
   * @param request the method, range, Accept and headers
   * @param handler the body handler of a 2xx response
   * @param <T> the body type
   * @return the response
   */
  public <T> CompletableFuture<MediaResponse<T>> media(
      String url, MediaRequest request, BodyHandler<T> handler) {
    return send(core.abs(url), request, handler);
  }

  /**
   * {@code GET} a processed image.
   *
   * @param source the source URL
   * @param options the processing options
   * @return the response
   */
  public CompletableFuture<MediaResponse<byte[]>> image(String source, ProcessingOptions options) {
    return image(source, options, UrlParams.of(), MediaRequest.get());
  }

  /**
   * {@code GET} (or {@code HEAD}) a processed image.
   *
   * @param source the source URL
   * @param options the processing options
   * @param params the extension, source encoding and PDF password
   * @param request the method, Accept and headers
   * @return the response
   */
  public CompletableFuture<MediaResponse<byte[]>> image(
      String source, ProcessingOptions options, UrlParams params, MediaRequest request) {
    return start(() -> media(core.urls.url(source, options, params), request));
  }

  /**
   * {@code GET} a {@code /video} result.
   *
   * @param source the source video
   * @param options the {@code /video} options
   * @param extension the output container; null for the server's default
   * @return the response
   */
  public CompletableFuture<MediaResponse<byte[]>> video(
      String source, VideoOptions options, @Nullable String extension) {
    return video(source, options, UrlParams.of().withExtension(extension), MediaRequest.get());
  }

  /**
   * {@code GET} (or {@code HEAD}) a {@code /video} result; a range asks for part of it (206).
   *
   * @param source the source video
   * @param options the {@code /video} options
   * @param params the container and source encoding
   * @param request the method, range, Accept and headers
   * @return the response
   */
  public CompletableFuture<MediaResponse<byte[]>> video(
      String source, VideoOptions options, UrlParams params, MediaRequest request) {
    return start(() -> media(core.urls.videoUrl(source, options, params), request));
  }

  /**
   * {@code GET} an HLS or DASH manifest.
   *
   * @param source the source video
   * @param stream the protocol, renditions, segment settings and other options
   * @return the manifest
   */
  public CompletableFuture<StreamManifest> stream(String source, StreamParams stream) {
    return start(
        () -> {
          String url = core.urls.streamUrl(source, stream);
          String ctype = ClientCore.manifestType(stream.protocol());
          return send(
                  url,
                  MediaRequest.get().withAccept(ctype + ", */*;q=0.5"),
                  BodyHandlers.ofByteArray())
              .thenApply(
                  res ->
                      new StreamManifest(
                          url, res.contentType().orElse(ctype), ClientCore.text(res.body())));
        });
  }

  /**
   * {@code GET} the playground's {@code api/meta}.
   *
   * @return the decoded JSON object
   */
  public CompletableFuture<Map<String, @Nullable Object>> playgroundMeta() {
    return send(
            core.playgroundMeta,
            MediaRequest.get().withAccept("application/json"),
            BodyHandlers.ofByteArray())
        .thenApply(ClientCore::jsonObject);
  }
}
