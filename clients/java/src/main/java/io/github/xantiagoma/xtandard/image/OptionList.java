package io.github.xantiagoma.xtandard.image;

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import org.jspecify.annotations.Nullable;

/**
 * The options of one pipeline, in URL order: what {@link ProcessingOptions}, {@link InfoOptions}
 * and {@link VideoOptions} share. Mutable and not thread-safe, like a {@link StringBuilder}.
 *
 * @param <S> the concrete options type
 */
public abstract sealed class OptionList<S extends OptionList<S>>
    permits ProcessingOptions, InfoOptions, VideoOptions {
  private final List<OptionEntry> entries = new ArrayList<>();

  OptionList() {}

  abstract S self();

  /**
   * Appends one option occurrence.
   *
   * @param name an option name or alias
   * @param args the arguments: strings, numbers, booleans or null (an empty argument)
   * @return this
   */
  public S option(String name, @Nullable Object @Nullable ... args) {
    entries.add(OptionEntry.of(name, args));
    return self();
  }

  /**
   * Appends one option occurrence.
   *
   * @param entry the option
   * @return this
   */
  public S option(OptionEntry entry) {
    entries.add(entry);
    return self();
  }

  /**
   * The options so far, in order.
   *
   * @return an unmodifiable view
   */
  public List<OptionEntry> entries() {
    return Collections.unmodifiableList(entries);
  }

  /**
   * Whether no option was added.
   *
   * @return true when empty
   */
  public boolean isEmpty() {
    return entries.isEmpty();
  }

  @Override
  public String toString() {
    return getClass().getSimpleName() + entries;
  }
}
