/// Source URL and PDF password encryption, byte-compatible with the server
/// (internal/security/crypt).
///
/// `token = base64url_unpadded(IV[16] ‖ AES-CBC(key, IV, PKCS#7(utf8(plaintext))))`.
library;

import 'dart:math';
import 'dart:typed_data';

import 'package:pointycastle/api.dart'
    show KeyParameter, PaddedBlockCipherParameters, ParametersWithIV;
import 'package:pointycastle/block/aes.dart';
import 'package:pointycastle/block/modes/cbc.dart';
import 'package:pointycastle/padded_block_cipher/padded_block_cipher_impl.dart';
import 'package:pointycastle/paddings/pkcs7.dart';

import 'errors.dart';
import 'generated/constants.g.dart';
import 'text.dart';

/// An encryption key: the first of `XI_ENCRYPTION_KEYS` (imgproxy
/// `IMGPROXY_SOURCE_URL_ENCRYPTION_KEY`).
///
/// The key decrypts every `enc/` source and PDF password. In an app it can
/// be extracted from the binary or the JavaScript bundle, so encrypt on
/// your backend when the sources are secret.
final class EncryptionKey {
  /// A key (16, 24 or 32 bytes) and an optional fixed 16-byte IV, as hex.
  ///
  /// Leave [iv] null for a fresh random IV per token, as the server's own
  /// encoder does. A fixed IV makes URLs deterministic and reveals when two
  /// URLs share a source; use it only for tests or cache keys. Throws
  /// [UrlBuildException] (`invalid_key`) for bad material.
  factory EncryptionKey({required String key, String? iv}) =>
      EncryptionKey.fromBytes(
        hexBytes(key, 'encryption key'),
        iv: iv == null ? null : hexBytes(iv, 'iv'),
      );

  /// A key and an optional fixed IV as raw bytes (copied).
  EncryptionKey.fromBytes(List<int> key, {List<int>? iv})
      : _key = Uint8List.fromList(key),
        _iv = iv == null ? null : Uint8List.fromList(iv) {
    if (!encryptionKeySizes.contains(_key.length)) {
      throw UrlBuildException(
        ErrorCode.invalidKey,
        'encryption key is ${_key.length} bytes, want 16, 24 or 32',
      );
    }
    if (_iv != null && _iv.length != ivSize) {
      throw const UrlBuildException(
        ErrorCode.invalidKey,
        'iv is not $ivSize bytes',
      );
    }
  }

  final Uint8List _key;
  final Uint8List? _iv;

  /// Whether the IV is fixed (deterministic tokens).
  bool get hasFixedIv => _iv != null;

  /// Hides the key material.
  @override
  String toString() => 'EncryptionKey(${_key.length * 8}-bit)';
}

final Random _random = Random.secure();

String _seal(String plaintext, EncryptionKey k) {
  final iv = k._iv ??
      Uint8List.fromList(
          List<int>.generate(ivSize, (_) => _random.nextInt(256)));
  final cipher =
      PaddedBlockCipherImpl(PKCS7Padding(), CBCBlockCipher(AESEngine()))
        ..init(
          true,
          PaddedBlockCipherParameters(
            ParametersWithIV(KeyParameter(k._key), iv),
            null,
          ),
        );
  final sealed = cipher.process(utf8Bytes(plaintext));
  return b64url([...iv, ...sealed]);
}

/// Encrypts a source URL into an `enc/` token.
///
/// Throws [UrlBuildException] (`invalid_source`) for an empty source or one
/// with control characters.
String encryptSource(String source, EncryptionKey key) =>
    _seal(checkText(source, ErrorCode.invalidSource, 'source'), key);

/// Encrypts the password of a password-protected PDF into a
/// `pdf_password` (`pdp`) token.
///
/// The token is the `enc/` format over the password, with the same key.
/// The server accepts the option on signed URLs only. Throws
/// [UrlBuildException] (`invalid_password`) for an empty password, one
/// with control characters, or one over [maxPdfPasswordBytes] UTF-8 bytes.
String encryptPdfPassword(String password, EncryptionKey key) {
  checkText(password, ErrorCode.invalidPassword, 'password');
  if (utf8Bytes(password).length > maxPdfPasswordBytes) {
    throw const UrlBuildException(
      ErrorCode.invalidPassword,
      'password is longer than $maxPdfPasswordBytes bytes',
    );
  }
  return _seal(password, key);
}
