/// Option catalogues at run time and their serialisation into option
/// segments.
library;

import 'errors.dart';
import 'generated/catalogue.g.dart';
import 'text.dart';

/// How option names are written.
enum OptionNames {
  /// The shortest alias (`rs`, `q`): the default.
  short,

  /// The canonical name (`resize`, `quality`).
  canonical,

  /// The name or alias as given.
  given,
}

/// One option occurrence: a name (canonical or alias) and its arguments.
typedef OptionEntry = ({String name, List<Object?> args});

/// One option catalogue: every name and alias to its canonical name, and
/// canonical to short names.
final class Catalogue {
  const Catalogue._(this.kind, this.names, this.shortNames);

  /// The processing options (`/…` and `o=`).
  static const processing =
      Catalogue._('imgproxy', processingNames, processingShortNames);

  /// The `/info` options.
  static const info = Catalogue._('info', infoNames, infoShortNames);

  /// The `/video` options.
  static const video = Catalogue._('video', videoNames, videoShortNames);

  /// The catalogue's name in error messages.
  final String kind;

  /// Every name and alias to its canonical name.
  final Map<String, String> names;

  /// Canonical name to the name builders write by default.
  final Map<String, String> shortNames;
}

/// Converts an options map into entries, in key order.
///
/// A value is a list of arguments, or the first argument alone; a null
/// value skips the option.
List<OptionEntry> entriesOf(Map<String, Object?> options) => [
      for (final MapEntry(:key, :value) in options.entries)
        if (value != null)
          (
            name: key,
            args: value is List
                ? [for (final a in value) checkArgType(key, a)]
                : [checkArgType(key, value)],
          ),
    ];

/// Checks that an argument is a `String`, a number, a `bool` or null.
Object? checkArgType(String name, Object? a) {
  if (a == null || a is String || a is num || a is bool) return a;
  throw UrlBuildException(
    ErrorCode.invalidArgument,
    'option $name: unsupported argument type ${a.runtimeType}',
  );
}

/// One entry list per pipeline: [options] is the first pipeline,
/// [pipelines] are chained (`/-/`) after it.
List<List<OptionEntry>> pipelinesOf(
  Map<String, Object?>? options,
  List<Map<String, Object?>>? pipelines,
) {
  final out = <List<OptionEntry>>[
    if (options != null) entriesOf(options),
    for (final p in pipelines ?? const <Map<String, Object?>>[]) entriesOf(p),
  ];
  return out.isEmpty ? [[]] : out;
}

// Characters an option argument cannot contain besides the separator.
final RegExp _badArg = RegExp('[/?#\\s\\x00-\\x1f\\x7f\\ufeff]');

/// Formats one argument.
///
/// Strings are checked, never escaped: the server does not unescape option
/// arguments.
String formatArg(Object? a, String sep, String where) {
  switch (a) {
    case null:
      return '';
    case final bool b:
      return b ? '1' : '0';
    case final num n:
      return formatNumber(n, where);
    case final String s:
      if (s.contains(sep) || _badArg.hasMatch(s)) {
        throw UrlBuildException(
          ErrorCode.invalidArgument,
          '$where: argument "$s" contains a character that cannot appear '
          'in an option',
        );
      }
      return s;
    default:
      throw UrlBuildException(
        ErrorCode.invalidArgument,
        '$where: unsupported argument type ${a.runtimeType}',
      );
  }
}

/// The name an option is written with; throws `unknown_option` for a name
/// not in [cat].
String writtenName(Catalogue cat, String name, OptionNames style) {
  final canonical = cat.names[name];
  if (canonical == null) {
    throw UrlBuildException(
      ErrorCode.unknownOption,
      'unknown ${cat.kind} option "$name"',
    );
  }
  return switch (style) {
    OptionNames.given => name,
    OptionNames.canonical => canonical,
    OptionNames.short => cat.shortNames[canonical] ?? canonical,
  };
}

/// Formats one option segment (`rs:fill:300:400`).
String formatEntry(
  Catalogue cat,
  OptionEntry entry,
  String sep,
  OptionNames style,
) {
  final (:name, :args) = entry;
  final written = writtenName(cat, name, style);
  if (args.isEmpty) {
    throw UrlBuildException(
      ErrorCode.invalidArgument,
      'option $name: at least one argument is required',
    );
  }
  return [
    written,
    for (var i = 0; i < args.length; i++)
      formatArg(args[i], sep, 'option $name argument ${i + 1}'),
  ].join(sep);
}
