package io.github.xantiagoma.xtandard.image;

import java.lang.reflect.Array;
import org.jspecify.annotations.Nullable;

/** Argument helpers of the generated option methods. */
final class Args {
  private Args() {}

  /**
   * The fixed arguments followed by the elements of a varargs array (of any component type,
   * primitives included); a null array adds nothing.
   */
  static @Nullable Object[] concat(@Nullable Object[] fixed, @Nullable Object rest) {
    int n = rest == null ? 0 : Array.getLength(rest);
    @Nullable Object[] out = new @Nullable Object[fixed.length + n];
    System.arraycopy(fixed, 0, out, 0, fixed.length);
    for (int i = 0; i < n; i++) {
      out[fixed.length + i] = Array.get(rest, i);
    }
    return out;
  }
}
