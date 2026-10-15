package io.github.xantiagoma.xtandard.image;

import java.math.BigDecimal;
import java.math.BigInteger;
import java.math.MathContext;
import java.math.RoundingMode;
import java.nio.ByteBuffer;
import java.nio.CharBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CharsetEncoder;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import java.util.List;
import java.util.regex.Pattern;

/** Byte and text helpers shared by signing, encryption and the URL builders. */
final class Text {
  private Text() {}

  private static final Pattern EXTENSION = Pattern.compile(Constants.EXTENSION_PATTERN);
  private static final BigInteger MAX_PLAIN_INTEGER = BigInteger.TEN.pow(21);
  private static final char[] HEX = "0123456789ABCDEF".toCharArray();
  private static final byte[] REPLACEMENT = {(byte) 0xEF, (byte) 0xBF, (byte) 0xBD};

  /** Decodes hex key material (either case, surrounding whitespace ignored). */
  static byte[] hex(String s, String what) {
    String t = s.strip();
    if (t.length() % 2 != 0) {
      throw new UrlBuildException(ErrorCode.INVALID_KEY, what + " is not a hex string");
    }
    byte[] out = new byte[t.length() / 2];
    for (int i = 0; i < out.length; i++) {
      int hi = Character.digit(t.charAt(2 * i), 16);
      int lo = Character.digit(t.charAt(2 * i + 1), 16);
      if (hi < 0 || lo < 0 || !isAsciiHex(t.charAt(2 * i)) || !isAsciiHex(t.charAt(2 * i + 1))) {
        throw new UrlBuildException(ErrorCode.INVALID_KEY, what + " is not a hex string");
      }
      out[i] = (byte) ((hi << 4) | lo);
    }
    return out;
  }

  private static boolean isAsciiHex(char c) {
    return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F');
  }

  /** Unpadded base64url (RFC 4648 section 5). */
  static String b64url(byte[] b) {
    return Base64.getUrlEncoder().withoutPadding().encodeToString(b);
  }

  /** Reports whether s contains an ASCII control character (C0 or DEL). */
  static boolean hasControlChar(String s) {
    for (int i = 0; i < s.length(); i++) {
      char c = s.charAt(i);
      if (c < 0x20 || c == 0x7f) {
        return true;
      }
    }
    return false;
  }

  /** Rejects an empty string or one with control characters. */
  static String checkText(String s, ErrorCode code, String what) {
    if (s.isEmpty() || hasControlChar(s)) {
      throw new UrlBuildException(
          code, what + " must be a non-empty string without control characters");
    }
    return s;
  }

  /** Rejects an extension that is not alphanumeric. */
  static String checkExtension(String ext) {
    if (!EXTENSION.matcher(ext).matches()) {
      throw new UrlBuildException(ErrorCode.INVALID_EXTENSION, "invalid extension \"" + ext + "\"");
    }
    return ext;
  }

  /** Rejects a separator that is not one character or that the grammar uses. */
  static String checkSeparator(String sep, List<String> forbidden) {
    if (sep.codePointCount(0, sep.length()) != 1 || forbidden.contains(sep)) {
      throw new UrlBuildException(
          ErrorCode.INVALID_SEPARATOR, "invalid argument separator \"" + sep + "\"");
    }
    return sep;
  }

  /** UTF-8 bytes of s; lone surrogates become U+FFFD, as with JavaScript's TextEncoder. */
  static byte[] utf8(String s) {
    CharsetEncoder enc =
        StandardCharsets.UTF_8
            .newEncoder()
            .onMalformedInput(CodingErrorAction.REPLACE)
            .onUnmappableCharacter(CodingErrorAction.REPLACE)
            .replaceWith(REPLACEMENT);
    try {
      ByteBuffer bb = enc.encode(CharBuffer.wrap(s));
      byte[] out = new byte[bb.remaining()];
      bb.get(out);
      return out;
    } catch (CharacterCodingException e) {
      throw new IllegalStateException("UTF-8 encoding with replacement failed", e);
    }
  }

  /** Characters a plain source keeps besides ASCII letters and digits. */
  static String escapePlain(String s) {
    return escape(s, Constants.PLAIN_SAFE);
  }

  /** Characters a query key or value keeps besides ASCII letters and digits. */
  static String escapeQuery(String s) {
    return escape(s, Constants.QUERY_SAFE);
  }

  private static String escape(String s, String safe) {
    StringBuilder out = new StringBuilder(s.length() + 16);
    for (int i = 0; i < s.length(); ) {
      int cp = s.codePointAt(i);
      int n = Character.charCount(cp);
      if (cp < 0x80
          && ((cp >= 'a' && cp <= 'z')
              || (cp >= 'A' && cp <= 'Z')
              || (cp >= '0' && cp <= '9')
              || safe.indexOf(cp) >= 0)) {
        out.append((char) cp);
      } else {
        for (byte b : utf8(s.substring(i, i + n))) {
          out.append('%').append(HEX[(b >> 4) & 0xf]).append(HEX[b & 0xf]);
        }
      }
      i += n;
    }
    return out.toString();
  }

  /**
   * Formats a number like ECMAScript {@code Number::toString} (300, 1.5, 0.1, 1e+21, 1e-7), so URLs
   * and their signatures match across languages. Integral types are written exactly.
   */
  static String formatNumber(Number v, String where) {
    if (v instanceof Long || v instanceof Integer || v instanceof Short || v instanceof Byte) {
      return Long.toString(v.longValue());
    }
    if (v instanceof BigInteger b) {
      if (b.abs().compareTo(MAX_PLAIN_INTEGER) < 0) {
        return b.toString();
      }
      return formatDouble(b.doubleValue(), where);
    }
    if (v instanceof Float f) {
      // The float as its user wrote it (1.1f is 1.1, not 1.100000023841858).
      return formatDouble(Double.parseDouble(Float.toString(f)), where);
    }
    return formatDouble(v.doubleValue(), where);
  }

  static String formatDouble(double v, String where) {
    if (!Double.isFinite(v)) {
      throw new UrlBuildException(
          ErrorCode.INVALID_ARGUMENT, where + ": " + v + " is not a finite number");
    }
    if (v == 0) {
      return "0";
    }
    String sign = v < 0 ? "-" : "";
    double a = Math.abs(v);
    // The shortest decimal that reads back as the same double (ECMAScript's k digits).
    BigDecimal exact = new BigDecimal(a);
    BigDecimal shortest = exact;
    for (int p = 1; p <= 17; p++) {
      BigDecimal r = exact.round(new MathContext(p, RoundingMode.HALF_EVEN));
      if (r.doubleValue() == a) {
        shortest = r;
        break;
      }
    }
    shortest = shortest.stripTrailingZeros();
    String digits = shortest.unscaledValue().toString();
    int k = digits.length();
    // a = 0.<digits> x 10^n
    int n = k - shortest.scale();
    String out;
    if (k <= n && n <= 21) {
      out = digits + "0".repeat(n - k);
    } else if (0 < n && n <= 21) {
      out = digits.substring(0, n) + "." + digits.substring(n);
    } else if (-6 < n && n <= 0) {
      out = "0." + "0".repeat(-n) + digits;
    } else {
      int e = n - 1;
      String es = "e" + (e >= 0 ? "+" : "-") + Math.abs(e);
      out = k == 1 ? digits + es : digits.charAt(0) + "." + digits.substring(1) + es;
    }
    return sign + out;
  }
}
