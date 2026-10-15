package io.github.xantiagoma.xtandard.image;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import org.jspecify.annotations.Nullable;

/**
 * One option occurrence: a name (canonical or alias) and its arguments.
 *
 * @param name the option name or alias, as the caller wrote it
 * @param args the arguments: strings, numbers, booleans, or null for an empty argument
 */
public record OptionEntry(String name, List<@Nullable Object> args) {

  /**
   * Copies the arguments into an unmodifiable list (nulls allowed).
   *
   * @param name the option name or alias
   * @param args the arguments
   */
  public OptionEntry {
    args = Collections.unmodifiableList(new ArrayList<>(args));
  }

  /**
   * An entry from varargs; a null array is one null (empty) argument.
   *
   * @param name the option name or alias
   * @param args the arguments
   * @return the entry
   */
  public static OptionEntry of(String name, @Nullable Object @Nullable ... args) {
    List<@Nullable Object> list =
        args == null ? Collections.singletonList(null) : Arrays.asList(args);
    return new OptionEntry(name, list);
  }
}
