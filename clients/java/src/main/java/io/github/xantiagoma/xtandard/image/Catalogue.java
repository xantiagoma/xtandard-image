package io.github.xantiagoma.xtandard.image;

import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;
import org.jspecify.annotations.Nullable;

/**
 * One option catalogue at run time, and the serialisation of options into option segments.
 *
 * @param kind the catalogue, for messages
 * @param names every name and alias to its canonical name
 * @param shortNames canonical name to short name
 */
record Catalogue(String kind, Map<String, String> names, Map<String, String> shortNames) {

  static final Catalogue PROCESSING =
      new Catalogue("imgproxy", OptionTables.PROCESSING_NAMES, OptionTables.PROCESSING_SHORT);
  static final Catalogue INFO =
      new Catalogue("info", OptionTables.INFO_NAMES, OptionTables.INFO_SHORT);
  static final Catalogue VIDEO =
      new Catalogue("video", OptionTables.VIDEO_NAMES, OptionTables.VIDEO_SHORT);

  /** Characters an option argument cannot contain besides the separator. */
  private static final Pattern BAD_ARG =
      Pattern.compile("[/?#\\s\\x00-\\x1f\\x7f\\ufeff]", Pattern.UNICODE_CHARACTER_CLASS);

  /** The canonical name of an option; unknown names are an error. */
  String canonical(String name) {
    String c = names.get(name);
    if (c == null) {
      throw new UrlBuildException(
          ErrorCode.UNKNOWN_OPTION, "unknown " + kind + " option \"" + name + "\"");
    }
    return c;
  }

  /** The name an option is written with. */
  String written(String name, OptionNameStyle style) {
    String c = canonical(name);
    return switch (style) {
      case GIVEN -> name;
      case CANONICAL -> c;
      case SHORT -> shortNames.getOrDefault(c, c);
    };
  }

  /** Formats one option segment ({@code rs:fill:300:400}). */
  String format(OptionEntry e, String sep, OptionNameStyle style) {
    String written = written(e.name(), style);
    List<@Nullable Object> args = e.args();
    if (args.isEmpty()) {
      throw new UrlBuildException(
          ErrorCode.INVALID_ARGUMENT, "option " + e.name() + ": at least one argument is required");
    }
    StringBuilder b = new StringBuilder(written);
    for (int i = 0; i < args.size(); i++) {
      b.append(sep).append(formatArg(args.get(i), sep, where(e, i)));
    }
    return b.toString();
  }

  static String where(OptionEntry e, int i) {
    return "option " + e.name() + " argument " + (i + 1);
  }

  /**
   * Formats one argument. Strings are checked, never escaped: the server does not unescape option
   * arguments.
   */
  static String formatArg(@Nullable Object a, String sep, String where) {
    if (a == null) {
      return "";
    }
    if (a instanceof Boolean b) {
      return b ? "1" : "0";
    }
    if (a instanceof Number n) {
      return Text.formatNumber(n, where);
    }
    if (a instanceof CharSequence || a instanceof Enum) {
      String s = a.toString();
      if (s.contains(sep) || BAD_ARG.matcher(s).find()) {
        throw new UrlBuildException(
            ErrorCode.INVALID_ARGUMENT,
            where
                + ": argument \""
                + s
                + "\" contains a character that cannot appear in an option");
      }
      return s;
    }
    throw new UrlBuildException(
        ErrorCode.INVALID_ARGUMENT,
        where + ": unsupported argument type " + a.getClass().getSimpleName());
  }
}
