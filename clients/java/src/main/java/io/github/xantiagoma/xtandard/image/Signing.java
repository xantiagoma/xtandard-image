package io.github.xantiagoma.xtandard.image;

import java.security.GeneralSecurityException;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.regex.Pattern;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import org.jspecify.annotations.Nullable;

/**
 * URL signing, byte-compatible with the server ({@code internal/security/sign}).
 *
 * <ul>
 *   <li>imgproxy: {@code base64url(HMAC-SHA256(key, salt || path)[:size])}, the signature segment
 *       of {@code /{signature}/{options}/{source}}; {@code /video} URLs sign {@code "/video" +
 *       path}.
 *   <li>{@code xi_sig}: the same construction over {@code path || "?" || query-without-xi_sig},
 *       appended as the last query parameter.
 * </ul>
 */
public final class Signing {
  private Signing() {}

  private static final Pattern ABSOLUTE = Pattern.compile("^[a-zA-Z][a-zA-Z0-9+.-]*://");

  /**
   * Signs a message with the salted HMAC-SHA256 construction of the imgproxy and {@code xi_sig}
   * schemes. Like the server, a message without a leading {@code /} is signed as if it had one.
   *
   * @param message the message (an imgproxy path, or an {@code xi_sig} message)
   * @param key the signing key
   * @return the unpadded base64url signature
   */
  public static String sign(String message, SigningKey key) {
    String m = message.startsWith("/") ? message : "/" + message;
    try {
      Mac mac = Mac.getInstance("HmacSHA256");
      // SecretKeySpec refuses an empty key; HMAC pads keys with zeros, so one zero byte is the
      // same key.
      byte[] k = key.key().length == 0 ? new byte[1] : key.key();
      mac.init(new SecretKeySpec(k, "HmacSHA256"));
      mac.update(key.salt());
      byte[] digest = mac.doFinal(Text.utf8(m));
      return Text.b64url(Arrays.copyOf(digest, key.size()));
    } catch (GeneralSecurityException e) {
      throw new IllegalStateException("HmacSHA256 is not available", e);
    }
  }

  /**
   * Signs an imgproxy path (everything after the signature segment).
   *
   * @param path the path, with or without its leading {@code /}
   * @param key the signing key
   * @return {@code "/" + signature + path}
   */
  public static String signImgproxyPath(String path, SigningKey key) {
    String p = path.startsWith("/") ? path : "/" + path;
    return "/" + sign(p, key) + p;
  }

  /**
   * A raw query split into its {@code xi_sig} value and the message {@code xi_sig} signs.
   *
   * @param signature the {@code xi_sig} value, or null when there is none
   * @param message the path, then {@code ?} and the remaining non-empty parameters in their
   *     original order and encoding (no {@code ?} when none remain)
   */
  public record XiMessage(@Nullable String signature, String message) {}

  /**
   * Splits a raw query into its {@code xi_sig} value and the message {@code xi_sig} signs.
   *
   * @param path the escaped path the dialect sees, after its mount prefix
   * @param rawQuery the query, without {@code ?}
   * @return the signature and the message
   * @throws UrlBuildException {@link ErrorCode#INVALID_REQUEST} when the query has two {@code
   *     xi_sig} (the server answers 403)
   */
  public static XiMessage xiMessage(String path, String rawQuery) {
    String sig = null;
    List<String> kept = new ArrayList<>();
    for (String part : rawQuery.split("&", -1)) {
      if (part.isEmpty()) {
        continue;
      }
      int eq = part.indexOf('=');
      String k = eq < 0 ? part : part.substring(0, eq);
      if (k.equals(Constants.XI_SIG_PARAM)) {
        if (sig != null) {
          throw new UrlBuildException(
              ErrorCode.INVALID_REQUEST,
              "query has more than one " + Constants.XI_SIG_PARAM + " parameter");
        }
        sig = eq < 0 ? "" : part.substring(eq + 1);
        continue;
      }
      kept.add(part);
    }
    return new XiMessage(sig, kept.isEmpty() ? path : path + "?" + String.join("&", kept));
  }

  /**
   * Re-signs a raw query: drops any {@code xi_sig} and appends a fresh one for the path.
   *
   * @param path the escaped path the dialect sees, after its mount prefix
   * @param rawQuery the query, with or without its leading {@code ?}
   * @param key the signing key
   * @return the query (without {@code ?}) ending in {@code xi_sig=...}
   */
  public static String signXiQuery(String path, String rawQuery, SigningKey key) {
    String q = rawQuery.startsWith("?") ? rawQuery.substring(1) : rawQuery;
    String msg = xiMessage(path, q).message();
    String sig = sign(msg, key);
    String rest = msg.substring(path.length());
    return rest.isEmpty()
        ? Constants.XI_SIG_PARAM + "=" + sig
        : rest.substring(1) + "&" + Constants.XI_SIG_PARAM + "=" + sig;
  }

  /**
   * Signs a URL (absolute, or a path with an optional query) with {@code xi_sig}, for a dialect
   * mounted at the root.
   *
   * @param url the URL; a fragment is dropped
   * @param key the signing key
   * @return the signed URL
   */
  public static String signXiUrl(String url, SigningKey key) {
    return signXiUrl(url, key, "");
  }

  /**
   * Signs a URL (absolute, or a path with an optional query) with {@code xi_sig}.
   *
   * @param url the URL; a fragment is dropped
   * @param key the signing key
   * @param mount the dialect's mount prefix, which the signed path excludes ({@code "/q"} for the
   *     query dialect)
   * @return the signed URL
   * @throws UrlBuildException {@link ErrorCode#INVALID_REQUEST} when the path is not under the
   *     mount or the query has two {@code xi_sig}
   */
  public static String signXiUrl(String url, SigningKey key, String mount) {
    int hash = url.indexOf('#');
    String noHash = hash < 0 ? url : url.substring(0, hash);
    int qm = noHash.indexOf('?');
    String head = qm < 0 ? noHash : noHash.substring(0, qm);
    String query = qm < 0 ? "" : noHash.substring(qm + 1);
    int pathStart =
        ABSOLUTE.matcher(head).lookingAt() ? head.indexOf('/', head.indexOf("//") + 2) : 0;
    String origin = pathStart < 0 ? head : head.substring(0, pathStart);
    String path = pathStart < 0 ? "/" : head.substring(pathStart);
    String m = stripTrailingSlashes(mount);
    if (!m.isEmpty()) {
      if (!path.equals(m) && !path.startsWith(m + "/")) {
        throw new UrlBuildException(
            ErrorCode.INVALID_REQUEST, "path " + path + " is not under the mount " + m);
      }
      path = path.substring(m.length());
      if (path.isEmpty()) {
        path = "/";
      }
    }
    return origin + m + path + "?" + signXiQuery(path, query, key);
  }

  static String stripTrailingSlashes(String s) {
    int end = s.length();
    while (end > 0 && s.charAt(end - 1) == '/') {
      end--;
    }
    return s.substring(0, end);
  }
}
