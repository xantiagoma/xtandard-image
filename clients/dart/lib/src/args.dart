/// Helpers of the generated option constructors and methods.
library;

import 'text.dart';

/// Drops trailing null arguments past the first [required] ones, which are
/// the optional positional parameters a caller left out.
List<Object?> trimArgs(List<Object?> args, int required) {
  var n = args.length;
  while (n > required && args[n - 1] == null) {
    n--;
  }
  return n == args.length ? args : args.sublist(0, n);
}

/// Whether two argument lists are equal element by element.
bool argsEqual(List<Object?> a, List<Object?> b) {
  if (a.length != b.length) return false;
  for (var i = 0; i < a.length; i++) {
    if (a[i] != b[i]) return false;
  }
  return true;
}

/// An argument as an option writes it, without validation: booleans are
/// `1`/`0`, numbers as ECMAScript writes them, null is empty.
String describeArg(Object? a) => switch (a) {
      null => '',
      true => '1',
      false => '0',
      final num n when n.isFinite => formatNumber(n, 'argument'),
      _ => '$a',
    };
