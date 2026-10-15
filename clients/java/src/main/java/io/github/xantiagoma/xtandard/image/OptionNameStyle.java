package io.github.xantiagoma.xtandard.image;

/** How option names are written in URLs. */
public enum OptionNameStyle {
  /** Each option's short name, the shortest alias ({@code rs}, {@code q}); the default. */
  SHORT,
  /** Each option's canonical name ({@code resize}, {@code quality}). */
  CANONICAL,
  /** The name or alias the caller used. */
  GIVEN
}
