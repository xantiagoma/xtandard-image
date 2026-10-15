package io.github.xantiagoma.xtandard.image;

import java.util.regex.Pattern;

/**
 * Source references: the string a URL carries as its source. These helpers only join strings; the
 * URL builders escape or encode the result.
 *
 * <pre>
 * https://host/path         an http source
 * s3://bucket/key           gs://bucket/key   abs://container/key
 * local:///path             ftp://host/path   sftp://host/path
 * xi://&lt;source&gt;/&lt;key&gt;       a named source (XI_SOURCES)
 * </pre>
 */
public final class Sources {
  private Sources() {}

  private static final Pattern NAME = Pattern.compile(Constants.XI_NAME_PATTERN);
  private static final Pattern BAD_AUTHORITY =
      Pattern.compile("[/?#\\s]", Pattern.UNICODE_CHARACTER_CLASS);

  /**
   * A named source reference, {@code xi://<name>/<key>}. The key may be relative or, for an http
   * source, an absolute URL: {@code xiSource("photos", "2026/cat.jpg")} is {@code
   * xi://photos/2026/cat.jpg}.
   *
   * @param name the source name (XI_SOURCES)
   * @param key the key inside the source; leading slashes are dropped
   * @return the source reference
   * @throws UrlBuildException {@link ErrorCode#INVALID_NAME} for a bad name
   */
  public static String xiSource(String name, String key) {
    if (!NAME.matcher(name).matches()) {
      throw new UrlBuildException(ErrorCode.INVALID_NAME, "invalid source name \"" + name + "\"");
    }
    return "xi://" + name + "/" + stripLeadingSlashes(key);
  }

  /**
   * A source reference for a scheme: the bucket, container, host or source name, then the key.
   * {@code local} takes no authority ({@code local:///<path>}).
   *
   * @param scheme the scheme
   * @param authority the bucket, container, host[:port] or source name; empty for {@code local}
   * @param key the key or path; leading slashes are dropped
   * @return the source reference
   * @throws UrlBuildException {@link ErrorCode#INVALID_NAME} when the authority is missing, has
   *     {@code /}, {@code ?}, {@code #} or whitespace, or is given for {@code local}
   */
  public static String sourceUrl(SourceScheme scheme, String authority, String key) {
    return sourceUrl(scheme.value(), authority, key);
  }

  /**
   * A source reference for a scheme given by name ({@code s3}, {@code https}, {@code xi}, ...).
   *
   * @param scheme the scheme
   * @param authority the bucket, container, host[:port] or source name; empty for {@code local}
   * @param key the key or path; leading slashes are dropped
   * @return the source reference
   * @throws UrlBuildException {@link ErrorCode#INVALID_NAME} when the authority is missing, has
   *     {@code /}, {@code ?}, {@code #} or whitespace, or is given for {@code local}
   */
  public static String sourceUrl(String scheme, String authority, String key) {
    if (scheme.equals("xi")) {
      return xiSource(authority, key);
    }
    if (scheme.equals("local")) {
      if (!authority.isEmpty()) {
        throw new UrlBuildException(
            ErrorCode.INVALID_NAME, "local sources have no authority: local:///<path>");
      }
      return "local:///" + stripLeadingSlashes(key);
    }
    if (authority.isEmpty() || BAD_AUTHORITY.matcher(authority).find()) {
      throw new UrlBuildException(
          ErrorCode.INVALID_NAME, "invalid " + scheme + " authority \"" + authority + "\"");
    }
    return scheme + "://" + authority + "/" + stripLeadingSlashes(key);
  }

  /**
   * Percent-escapes a source for a {@code plain/} URL; the server path-unescapes it. ASCII letters,
   * digits, RFC 3986 unreserved characters, the sub-delims, {@code :} and {@code /} are kept;
   * everything else ({@code %}, {@code @}, {@code ?}, {@code #}, spaces, non-ASCII) is
   * percent-encoded as UTF-8.
   *
   * @param source the source
   * @return the escaped source
   */
  public static String escapePlainSource(String source) {
    return Text.escapePlain(source);
  }

  /**
   * Percent-escapes a query key or value, keeping {@code :}, {@code /} and {@code ,} readable.
   *
   * @param value the key or value
   * @return the escaped string
   */
  public static String escapeQueryValue(String value) {
    return Text.escapeQuery(value);
  }

  private static String stripLeadingSlashes(String s) {
    int i = 0;
    while (i < s.length() && s.charAt(i) == '/') {
      i++;
    }
    return s.substring(i);
  }
}
