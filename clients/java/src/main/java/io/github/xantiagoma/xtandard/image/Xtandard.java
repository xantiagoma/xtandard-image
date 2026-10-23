package io.github.xantiagoma.xtandard.image;

/**
 * Entry points, named like the TypeScript client's factories.
 *
 * <pre>{@code
 * import static io.github.xantiagoma.xtandard.image.Xtandard.*;
 *
 * ImgproxyUrlBuilder img = imgproxy().baseUrl("https://img.example.com").signing(key).build();
 * QueryUrlBuilder q = queryUrls().baseUrl("https://img.example.com").build();
 * XtandardClient xi = client("http://localhost:8080").build();
 * String src = xiSource("photos", "2026/cat.jpg");
 * }</pre>
 */
public final class Xtandard {
  private Xtandard() {}

  /**
   * Starts an imgproxy-dialect URL builder ({@link ImgproxyUrlBuilder#builder()}).
   *
   * @return a new builder
   */
  public static ImgproxyUrlBuilder.Builder imgproxy() {
    return ImgproxyUrlBuilder.builder();
  }

  /**
   * Starts a query dialect URL builder ({@link QueryUrlBuilder#builder()}).
   *
   * @return a new builder
   */
  public static QueryUrlBuilder.Builder queryUrls() {
    return QueryUrlBuilder.builder();
  }

  /**
   * Starts an HTTP client ({@link XtandardClient#builder(String)}).
   *
   * @param baseUrl the server origin, plus the imgproxy mount prefix if any
   * @return a new builder
   */
  public static XtandardClient.Builder client(String baseUrl) {
    return XtandardClient.builder(baseUrl);
  }

  /**
   * Splits a built query dialect URL into an HTTP QUERY request ({@link
   * QueryMethodRequest#from(String)}).
   *
   * @param url a query dialect URL with a query
   * @return the QUERY request
   */
  public static QueryMethodRequest queryMethodRequest(String url) {
    return QueryMethodRequest.from(url);
  }

  /**
   * A named source reference ({@link Sources#xiSource(String, String)}).
   *
   * @param name the source name
   * @param key the key inside the source
   * @return {@code xi://<name>/<key>}
   */
  public static String xiSource(String name, String key) {
    return Sources.xiSource(name, key);
  }
}
