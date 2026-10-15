package io.github.xantiagoma.xtandard.image;

import java.math.BigDecimal;
import java.math.BigInteger;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.jspecify.annotations.Nullable;

/**
 * A strict RFC 8259 JSON parser, so the library needs no JSON dependency. Values become {@link Map}
 * (insertion-ordered, unmodifiable), {@link List} (unmodifiable), {@link String}, {@link Long}
 * (integers that fit), {@link Double} (other numbers; {@link BigInteger} for integers beyond a
 * long), {@link Boolean} or null.
 */
final class Json {
  private static final int MAX_DEPTH = 512;

  private final String s;
  private int i;

  private Json(String s) {
    this.s = s;
  }

  /**
   * Parses one JSON document.
   *
   * @throws IllegalArgumentException the text is not JSON
   */
  static @Nullable Object parse(String text) {
    Json p = new Json(text);
    p.ws();
    Object v = p.value(0);
    p.ws();
    if (p.i != p.s.length()) {
      throw p.error("trailing characters");
    }
    return v;
  }

  private IllegalArgumentException error(String what) {
    return new IllegalArgumentException("invalid JSON at offset " + i + ": " + what);
  }

  private void ws() {
    while (i < s.length()) {
      char c = s.charAt(i);
      if (c != ' ' && c != '\t' && c != '\n' && c != '\r') {
        return;
      }
      i++;
    }
  }

  private @Nullable Object value(int depth) {
    if (depth > MAX_DEPTH) {
      throw error("nested too deeply");
    }
    if (i >= s.length()) {
      throw error("unexpected end");
    }
    char c = s.charAt(i);
    return switch (c) {
      case '{' -> object(depth);
      case '[' -> array(depth);
      case '"' -> string();
      case 't' -> {
        literal("true");
        yield true;
      }
      case 'f' -> {
        literal("false");
        yield false;
      }
      case 'n' -> {
        literal("null");
        yield null;
      }
      default -> {
        if (c == '-' || (c >= '0' && c <= '9')) {
          yield number();
        }
        throw error("unexpected character '" + c + "'");
      }
    };
  }

  private void literal(String word) {
    if (!s.startsWith(word, i)) {
      throw error("expected " + word);
    }
    i += word.length();
  }

  private Map<String, @Nullable Object> object(int depth) {
    i++; // {
    Map<String, @Nullable Object> out = new LinkedHashMap<>();
    ws();
    if (i < s.length() && s.charAt(i) == '}') {
      i++;
      return Collections.unmodifiableMap(out);
    }
    while (true) {
      ws();
      if (i >= s.length() || s.charAt(i) != '"') {
        throw error("expected a key");
      }
      String k = string();
      ws();
      expect(':');
      ws();
      out.put(k, value(depth + 1));
      ws();
      if (i < s.length() && s.charAt(i) == ',') {
        i++;
        continue;
      }
      expect('}');
      return Collections.unmodifiableMap(out);
    }
  }

  private List<@Nullable Object> array(int depth) {
    i++; // [
    List<@Nullable Object> out = new ArrayList<>();
    ws();
    if (i < s.length() && s.charAt(i) == ']') {
      i++;
      return Collections.unmodifiableList(out);
    }
    while (true) {
      ws();
      out.add(value(depth + 1));
      ws();
      if (i < s.length() && s.charAt(i) == ',') {
        i++;
        continue;
      }
      expect(']');
      return Collections.unmodifiableList(out);
    }
  }

  private void expect(char c) {
    if (i >= s.length() || s.charAt(i) != c) {
      throw error("expected '" + c + "'");
    }
    i++;
  }

  private String string() {
    i++; // "
    StringBuilder b = new StringBuilder();
    while (true) {
      if (i >= s.length()) {
        throw error("unterminated string");
      }
      char c = s.charAt(i++);
      if (c == '"') {
        return b.toString();
      }
      if (c < 0x20) {
        throw error("control character in string");
      }
      if (c != '\\') {
        b.append(c);
        continue;
      }
      if (i >= s.length()) {
        throw error("unterminated escape");
      }
      char e = s.charAt(i++);
      switch (e) {
        case '"', '\\', '/' -> b.append(e);
        case 'b' -> b.append('\b');
        case 'f' -> b.append('\f');
        case 'n' -> b.append('\n');
        case 'r' -> b.append('\r');
        case 't' -> b.append('\t');
        case 'u' -> b.append(unicodeEscape());
        default -> throw error("bad escape '\\" + e + "'");
      }
    }
  }

  private char unicodeEscape() {
    if (i + 4 > s.length()) {
      throw error("short unicode escape");
    }
    int cp = 0;
    for (int k = 0; k < 4; k++) {
      int d = Character.digit(s.charAt(i + k), 16);
      if (d < 0) {
        throw error("bad unicode escape");
      }
      cp = cp * 16 + d;
    }
    i += 4;
    return (char) cp;
  }

  private Object number() {
    int start = i;
    if (s.charAt(i) == '-') {
      i++;
    }
    if (i >= s.length()) {
      throw error("bad number");
    }
    if (s.charAt(i) == '0') {
      i++;
    } else if (s.charAt(i) >= '1' && s.charAt(i) <= '9') {
      digits();
    } else {
      throw error("bad number");
    }
    boolean integral = true;
    if (i < s.length() && s.charAt(i) == '.') {
      integral = false;
      i++;
      if (digits() == 0) {
        throw error("bad fraction");
      }
    }
    if (i < s.length() && (s.charAt(i) == 'e' || s.charAt(i) == 'E')) {
      integral = false;
      i++;
      if (i < s.length() && (s.charAt(i) == '+' || s.charAt(i) == '-')) {
        i++;
      }
      if (digits() == 0) {
        throw error("bad exponent");
      }
    }
    String t = s.substring(start, i);
    if (integral) {
      BigInteger b = new BigInteger(t);
      return b.bitLength() < 64 ? (Object) b.longValue() : b;
    }
    return new BigDecimal(t).doubleValue();
  }

  private int digits() {
    int n = 0;
    while (i < s.length() && s.charAt(i) >= '0' && s.charAt(i) <= '9') {
      i++;
      n++;
    }
    return n;
  }
}
