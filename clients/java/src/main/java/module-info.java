/**
 * The official Java client of xtandard-image: URL builders for the imgproxy and query dialects,
 * imgproxy and {@code xi_sig} signing, {@code enc/} source and PDF password encryption, and an HTTP
 * client on {@link java.net.http.HttpClient}.
 *
 * <p>It has no runtime dependencies. The JSpecify nullness annotations are only needed to compile
 * it ({@code requires static}).
 */
module io.github.xantiagoma.xtandard.image {
  requires transitive java.net.http;
  requires static transitive org.jspecify;

  exports io.github.xantiagoma.xtandard.image;
}
