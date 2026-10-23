/// Source references: the string a URL carries as its source.
///
/// These helpers only join strings; the URL builders escape or encode the
/// result:
///
/// ```text
/// https://host/path         an http source
/// s3://bucket/key           gs://bucket/key   abs://container/key
/// local:///path             ftp://host/path   sftp://host/path
/// xi://<source>/<key>       a named source (XI_SOURCES)
/// ```
library;

import 'errors.dart';
import 'generated/constants.g.dart';

final RegExp _name = RegExp(xiNamePattern);
final RegExp _badAuthority = RegExp(r'[/?#\s]');

String _key(String key) {
  var i = 0;
  while (i < key.length && key.codeUnitAt(i) == 0x2f) {
    i++;
  }
  return key.substring(i);
}

/// A named source reference, `xi://<name>/<key>`.
///
/// The key may be relative or, for an http source, an absolute URL:
///
/// ```dart
/// xiSource('photos', '2026/cat.jpg');               // xi://photos/2026/cat.jpg
/// xiSource('web', 'https://cdn.example.com/a.jpg'); // xi://web/https://cdn.example.com/a.jpg
/// ```
///
/// Throws [UrlBuildException] (`invalid_name`) when [name] is not a source
/// name.
String xiSource(String name, String key) {
  if (!_name.hasMatch(name)) {
    throw UrlBuildException(
      ErrorCode.invalidName,
      'invalid source name "$name"',
    );
  }
  return 'xi://$name/${_key(key)}';
}

/// A source reference for a scheme: the bucket, container, host or source
/// name, then the key.
///
/// `local` takes only a path (`local:///<path>`):
///
/// ```dart
/// sourceUrl(SourceScheme.s3, 'photos', 'cat.jpg');  // s3://photos/cat.jpg
/// sourceUrl(SourceScheme.local, '', 'img/cat.jpg'); // local:///img/cat.jpg
/// ```
///
/// Throws [UrlBuildException] (`invalid_name`) when the authority is
/// missing, has `/`, `?`, `#` or whitespace, or is given for `local`.
String sourceUrl(SourceScheme scheme, String authority, String key) {
  switch (scheme) {
    case SourceScheme.xi:
      return xiSource(authority, key);
    case SourceScheme.local:
      if (authority.isNotEmpty) {
        throw const UrlBuildException(
          ErrorCode.invalidName,
          'local sources have no authority: local:///<path>',
        );
      }
      return 'local:///${_key(key)}';
    default:
      if (authority.isEmpty || _badAuthority.hasMatch(authority)) {
        throw UrlBuildException(
          ErrorCode.invalidName,
          'invalid ${scheme.value} authority "$authority"',
        );
      }
      return '${scheme.value}://$authority/${_key(key)}';
  }
}
