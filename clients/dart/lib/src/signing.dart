/// URL signing, byte-compatible with the server (internal/security/sign).
///
/// * imgproxy: `base64url(HMAC-SHA256(key, salt ‖ path)[:size])`, the
///   signature segment of `/{signature}/{options}/{source}`; `/video` URLs
///   sign `"/video" + path`.
/// * xi_sig: the same construction over `path ‖ "?" ‖ query-without-xi_sig`,
///   appended as the last query parameter.
library;

import 'dart:typed_data';

import 'package:crypto/crypto.dart';

import 'errors.dart';
import 'generated/constants.g.dart';
import 'text.dart';

/// A signing key: `XI_SIGNING_KEYS` / `XI_SIGNING_SALTS` (imgproxy
/// `IMGPROXY_KEY` / `IMGPROXY_SALT`).
///
/// Whoever holds the key can sign any URL. In an app the key can be
/// extracted from the binary or the JavaScript bundle, so prefer URLs
/// signed by your backend, or the server's `restricted` unsigned policy.
final class SigningKey {
  /// A key and salt as hex strings, as in the server configuration.
  ///
  /// [size] is the signature size in bytes before encoding, 1 to 32
  /// (`XI_SIGNING_SIZE`). Throws [UrlBuildException] (`invalid_key`) for
  /// material that is not hex or a size out of range.
  factory SigningKey({
    required String key,
    required String salt,
    int size = defaultSignatureSize,
  }) =>
      SigningKey.fromBytes(
        hexBytes(key, 'signing key'),
        hexBytes(salt, 'signing salt'),
        size: size,
      );

  /// A key and salt as raw bytes (copied).
  SigningKey.fromBytes(
    List<int> key,
    List<int> salt, {
    this.size = defaultSignatureSize,
  })  : _key = Uint8List.fromList(key),
        _salt = Uint8List.fromList(salt) {
    if (size < minSignatureSize || size > maxSignatureSize) {
      throw UrlBuildException(
        ErrorCode.invalidKey,
        'signature size $size out of range '
        '[$minSignatureSize, $maxSignatureSize]',
      );
    }
  }

  final Uint8List _key;
  final Uint8List _salt;

  /// The signature size in bytes before encoding.
  final int size;

  /// Hides the key material.
  @override
  String toString() => 'SigningKey(size: $size)';
}

/// Signs [message] with the salted HMAC-SHA256 construction of the imgproxy
/// and xi_sig schemes.
///
/// Like the server, a message without a leading `/` is signed as if it had
/// one.
String sign(String message, SigningKey key) {
  final m = message.startsWith('/') ? message : '/$message';
  final mac =
      Hmac(sha256, key._key).convert([...key._salt, ...utf8Bytes(m)]).bytes;
  return b64url(mac.sublist(0, key.size));
}

/// Returns `"/" + signature + path` for an imgproxy path (everything after
/// the signature segment).
String signImgproxyPath(String path, SigningKey key) {
  final p = path.startsWith('/') ? path : '/$path';
  return '/${sign(p, key)}$p';
}

/// Splits a raw query into its xi_sig value and the message xi_sig signs.
///
/// The message is [path], then `?` and the remaining non-empty parameters
/// in their original order and encoding. Throws [UrlBuildException]
/// (`invalid_request`) when the query has more than one xi_sig (the server
/// answers 403).
({String? signature, String message}) xiMessage(String path, String rawQuery) {
  String? sig;
  final kept = <String>[];
  for (final part in rawQuery.split('&')) {
    if (part.isEmpty) continue;
    final eq = part.indexOf('=');
    final k = eq < 0 ? part : part.substring(0, eq);
    if (k == xiSigParam) {
      if (sig != null) {
        throw const UrlBuildException(
          ErrorCode.invalidRequest,
          'query has more than one $xiSigParam parameter',
        );
      }
      sig = eq < 0 ? '' : part.substring(eq + 1);
      continue;
    }
    kept.add(part);
  }
  return (
    signature: sig,
    message: kept.isEmpty ? path : '$path?${kept.join('&')}',
  );
}

/// Returns [rawQuery] without any xi_sig and with a fresh xi_sig for [path]
/// appended last.
///
/// [path] is the escaped path the dialect sees, after its mount prefix.
String signXiQuery(String path, String rawQuery, SigningKey key) {
  final q0 = rawQuery.startsWith('?') ? rawQuery.substring(1) : rawQuery;
  final msg = xiMessage(path, q0).message;
  final sig = sign(msg, key);
  final q = msg.substring(path.length);
  return q.isEmpty ? '$xiSigParam=$sig' : '${q.substring(1)}&$xiSigParam=$sig';
}

final RegExp _absolute = RegExp(r'^[a-zA-Z][a-zA-Z0-9+.-]*://');

/// Signs a URL (absolute, or a path with an optional query) with xi_sig.
///
/// [mount] is the dialect's mount prefix, which the signed path excludes
/// (`/q` for the query dialect). A fragment is dropped. Throws
/// [UrlBuildException] (`invalid_request`) when the path is not under the
/// mount or the query has two xi_sig.
String signXiUrl(String url, SigningKey key, {String mount = ''}) {
  final hash = url.indexOf('#');
  final noHash = hash < 0 ? url : url.substring(0, hash);
  final qi = noHash.indexOf('?');
  final head = qi < 0 ? noHash : noHash.substring(0, qi);
  final query = qi < 0 ? '' : noHash.substring(qi + 1);
  final pathStart =
      _absolute.hasMatch(head) ? head.indexOf('/', head.indexOf('//') + 2) : 0;
  final origin = pathStart < 0 ? head : head.substring(0, pathStart);
  var path = pathStart < 0 ? '/' : head.substring(pathStart);
  var m = mount;
  while (m.endsWith('/')) {
    m = m.substring(0, m.length - 1);
  }
  if (m.isNotEmpty) {
    if (path != m && !path.startsWith('$m/')) {
      throw UrlBuildException(
        ErrorCode.invalidRequest,
        'path $path is not under the mount $m',
      );
    }
    path = path.substring(m.length);
    if (path.isEmpty) path = '/';
  }
  return '$origin$m$path?${signXiQuery(path, query, key)}';
}
