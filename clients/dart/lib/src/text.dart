/// Byte and text helpers shared by signing, encryption and the builders.
library;

import 'dart:convert';
import 'dart:typed_data';

import 'errors.dart';
import 'generated/constants.g.dart';

final RegExp _hex = RegExp(r'^[0-9a-fA-F]*$');
final RegExp _control = RegExp(r'[\x00-\x1f\x7f]');
final RegExp _extension = RegExp(extensionPattern);

/// Decodes hex key material (either case; surrounding whitespace ignored).
Uint8List hexBytes(String hex, String what) {
  final s = hex.trim();
  if (s.length.isOdd || !_hex.hasMatch(s)) {
    throw UrlBuildException(ErrorCode.invalidKey, '$what is not a hex string');
  }
  final out = Uint8List(s.length ~/ 2);
  for (var i = 0; i < out.length; i++) {
    out[i] = int.parse(s.substring(2 * i, 2 * i + 2), radix: 16);
  }
  return out;
}

/// Unpadded base64url (RFC 4648 §5).
String b64url(List<int> bytes) {
  final s = base64Url.encode(bytes);
  final end = s.indexOf('=');
  return end < 0 ? s : s.substring(0, end);
}

/// UTF-8 bytes of [s]; lone surrogates become U+FFFD, as with TextEncoder.
Uint8List utf8Bytes(String s) => utf8.encode(s);

/// Whether [s] contains an ASCII control character (C0 or DEL).
bool hasControlChar(String s) => _control.hasMatch(s);

/// Rejects an empty string or one with control characters.
String checkText(String s, ErrorCode code, String what) {
  if (s.isEmpty || hasControlChar(s)) {
    throw UrlBuildException(
      code,
      '$what must be a non-empty string without control characters',
    );
  }
  return s;
}

/// Rejects an extension that is not alphanumeric.
String checkExtension(String ext) {
  if (!_extension.hasMatch(ext)) {
    throw UrlBuildException(
      ErrorCode.invalidExtension,
      'invalid extension "$ext"',
    );
  }
  return ext;
}

final Set<int> _plainKeep = _keepSet(plainSafe);
final Set<int> _queryKeep = _keepSet(querySafe);

Set<int> _keepSet(String safe) => {
      for (var c = 0x30; c <= 0x39; c++) c,
      for (var c = 0x41; c <= 0x5a; c++) c,
      for (var c = 0x61; c <= 0x7a; c++) c,
      ...safe.codeUnits,
    };

const String _hexDigits = '0123456789ABCDEF';

String _escape(String s, Set<int> keep) {
  final out = StringBuffer();
  for (final rune in s.runes) {
    if (keep.contains(rune)) {
      out.writeCharCode(rune);
      continue;
    }
    for (final b in utf8.encode(String.fromCharCode(rune))) {
      out
        ..write('%')
        ..write(_hexDigits[b >> 4])
        ..write(_hexDigits[b & 0xf]);
    }
  }
  return out.toString();
}

/// Percent-escapes a source for a `plain/` URL; the server path-unescapes
/// it.
///
/// ASCII letters, digits and [plainSafe] (the RFC 3986 unreserved
/// characters, the sub-delims, `:` and `/`) are kept; everything else
/// (`%`, `@`, `?`, `#`, spaces, non-ASCII) is written as `%XX` per UTF-8
/// byte.
String escapePlainSource(String source) => _escape(source, _plainKeep);

/// Percent-escapes a query key or value, keeping [querySafe] (`:`, `/`,
/// `,`, …) readable.
String escapeQueryValue(String value) => _escape(value, _queryKeep);

/// Formats a number like ECMAScript `Number::toString`: `300`, `1.5`,
/// `0.1`, `1e+21`, `1e-7`.
///
/// Every official client writes numbers this way, so URLs (and their
/// signatures) match across languages. Dart's `double.toString` already
/// picks the shortest round-trip digits and the same exponent thresholds;
/// on the VM it adds `.0` to whole numbers, which is dropped here.
String formatNumber(num v, String where) {
  // On the web every whole double is also an int, infinities included.
  if (!v.isFinite) {
    throw UrlBuildException(
      ErrorCode.invalidArgument,
      '$where: $v is not a finite number',
    );
  }
  // -0 is written 0.
  if (v == 0) return '0';
  if (v is int) return v.toString();
  final s = v.toString();
  return s.endsWith('.0') ? s.substring(0, s.length - 2) : s;
}
