package io.github.xantiagoma.xtandard.image;

import java.util.AbstractMap;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import org.jspecify.annotations.Nullable;

/**
 * Query keys and values in the order they are added: what {@link QueryParams} and {@link
 * ImaginaryParams} share. Keys may repeat; a null value is skipped. Mutable and not thread-safe.
 *
 * @param <S> the concrete type
 */
public abstract sealed class ParamList<S extends ParamList<S>>
    permits QueryParams, ImaginaryParams {
  private final List<Map.Entry<String, @Nullable Object>> pairs = new ArrayList<>();

  ParamList() {}

  abstract S self();

  /**
   * Appends a key; any key is allowed.
   *
   * @param key the key
   * @param value a string, number or boolean ({@code true}/{@code false}); null skips the key
   * @return this
   */
  public S param(String key, @Nullable Object value) {
    pairs.add(new AbstractMap.SimpleImmutableEntry<>(key, value));
    return self();
  }

  /**
   * The keys and values so far, in order (null values included).
   *
   * @return an unmodifiable view
   */
  public List<Map.Entry<String, @Nullable Object>> entries() {
    return Collections.unmodifiableList(pairs);
  }

  /** The non-null pairs, formatted (booleans true/false, numbers as ECMAScript writes them). */
  List<Map.Entry<String, String>> formatted() {
    List<Map.Entry<String, String>> out = new ArrayList<>();
    for (Map.Entry<String, @Nullable Object> p : pairs) {
      Object v = p.getValue();
      if (v != null) {
        out.add(new AbstractMap.SimpleImmutableEntry<>(p.getKey(), nativeValue(p.getKey(), v)));
      }
    }
    return out;
  }

  static String nativeValue(String key, Object v) {
    if (v instanceof Boolean b) {
      return b ? "true" : "false";
    }
    if (v instanceof Number n) {
      return Text.formatNumber(n, "query key " + key);
    }
    if (v instanceof CharSequence || v instanceof Enum) {
      return v.toString();
    }
    throw new UrlBuildException(
        ErrorCode.INVALID_ARGUMENT,
        "query key " + key + ": unsupported value type " + v.getClass().getSimpleName());
  }

  @Override
  public String toString() {
    return getClass().getSimpleName() + pairs;
  }
}
