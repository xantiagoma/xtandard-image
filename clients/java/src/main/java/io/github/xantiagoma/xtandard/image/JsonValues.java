package io.github.xantiagoma.xtandard.image;

import java.math.BigInteger;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.jspecify.annotations.Nullable;

/**
 * Typed access to parsed JSON values ({@link Json}), for the generated response records. Every
 * failure is an {@link IllegalArgumentException} naming the path.
 */
final class JsonValues {
  private JsonValues() {}

  /** Decodes one element; {@code where} names it in errors. */
  @FunctionalInterface
  interface Decoder<T> {
    T decode(@Nullable Object value, String where);
  }

  static IllegalArgumentException wrongType(String where, String want, @Nullable Object got) {
    String g = got == null ? "null" : got.getClass().getSimpleName();
    return new IllegalArgumentException(where + ": want " + want + ", got " + g);
  }

  @SuppressWarnings("unchecked")
  static Map<String, @Nullable Object> object(@Nullable Object v, String where) {
    if (v instanceof Map) {
      return (Map<String, @Nullable Object>) v;
    }
    throw wrongType(where, "an object", v);
  }

  static Object required(Map<String, @Nullable Object> o, String key, String where) {
    Object v = o.get(key);
    if (v == null) {
      throw new IllegalArgumentException(where + ": missing " + key);
    }
    return v;
  }

  static Object nonNull(@Nullable Object v, String where) {
    if (v == null) {
      throw wrongType(where, "a value", null);
    }
    return v;
  }

  static <T> @Nullable T optional(@Nullable Object v, String where, Decoder<T> d) {
    return v == null ? null : d.decode(v, where);
  }

  static String string(Object v, String where) {
    if (v instanceof String s) {
      return s;
    }
    throw wrongType(where, "a string", v);
  }

  static Long integer(Object v, String where) {
    if (v instanceof Long l) {
      return l;
    }
    if (v instanceof Double d) {
      if (d == Math.rint(d) && Math.abs(d) < 0x1p63) {
        return d.longValue();
      }
    }
    if (v instanceof BigInteger) {
      throw new IllegalArgumentException(where + ": integer out of range");
    }
    throw wrongType(where, "an integer", v);
  }

  static Double number(Object v, String where) {
    if (v instanceof Number n) {
      return n.doubleValue();
    }
    throw wrongType(where, "a number", v);
  }

  static Boolean bool(Object v, String where) {
    if (v instanceof Boolean b) {
      return b;
    }
    throw wrongType(where, "a boolean", v);
  }

  static <T> List<T> list(Object v, String where, Decoder<T> d) {
    if (!(v instanceof List<?> in)) {
      throw wrongType(where, "an array", v);
    }
    List<T> out = new ArrayList<>(in.size());
    for (int i = 0; i < in.size(); i++) {
      out.add(d.decode(in.get(i), where + "[" + i + "]"));
    }
    return Collections.unmodifiableList(out);
  }

  /** A JSON object with its values as parsed. */
  static Map<String, @Nullable Object> rawMap(Object v, String where) {
    return object(v, where);
  }

  /** A JSON array with its elements as parsed. */
  @SuppressWarnings("unchecked")
  static List<@Nullable Object> rawList(Object v, String where) {
    if (v instanceof List<?>) {
      return (List<@Nullable Object>) v;
    }
    throw wrongType(where, "an array", v);
  }

  static <T> Map<String, T> map(Object v, String where, Decoder<T> d) {
    Map<String, @Nullable Object> in = object(v, where);
    Map<String, T> out = new LinkedHashMap<>();
    for (Map.Entry<String, @Nullable Object> e : in.entrySet()) {
      out.put(e.getKey(), d.decode(e.getValue(), where + "." + e.getKey()));
    }
    return Collections.unmodifiableMap(out);
  }
}
