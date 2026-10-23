// testdata/security/vectors.json: the signing, enc/ and pdf_password
// vectors shared with Go, TypeScript and the other clients.

import 'dart:convert';
import 'dart:typed_data';

import 'package:pointycastle/api.dart'
    show KeyParameter, PaddedBlockCipherParameters, ParametersWithIV;
import 'package:pointycastle/block/aes.dart';
import 'package:pointycastle/block/modes/cbc.dart';
import 'package:pointycastle/padded_block_cipher/padded_block_cipher_impl.dart';
import 'package:pointycastle/paddings/pkcs7.dart';
import 'package:test/test.dart';
import 'package:xtandard_image/xtandard_image.dart';

import 'support/fixtures.dart';

Uint8List _hex(String s) => Uint8List.fromList([
      for (var i = 0; i < s.length; i += 2)
        int.parse(s.substring(i, i + 2), radix: 16),
    ]);

/// Decrypts a token the way the server does.
String decrypt(String token, String key) {
  final raw = base64Url.decode(base64Url.normalize(token));
  final cipher =
      PaddedBlockCipherImpl(PKCS7Padding(), CBCBlockCipher(AESEngine()))
        ..init(
          false,
          PaddedBlockCipherParameters(
            ParametersWithIV(KeyParameter(_hex(key)), raw.sublist(0, 16)),
            null,
          ),
        );
  return utf8.decode(cipher.process(raw.sublist(16)));
}

void vectorGroup(
  String section,
  void Function(Json v) run,
) =>
    group(section, () {
      fixtureTests(
        'testdata/security/vectors.json',
        cases: (f) => (f[section]! as List).cast<Json>(),
        name: (v) => '$section ${v['name']}',
        run: (_, v) => run(v),
      );
    });

void main() {
  vectorGroup('imgproxy', (v) {
    final path = v['path']! as String;
    final want = v['signature']! as String;
    final k = SigningKey(
      key: v['key']! as String,
      salt: v['salt']! as String,
      size: v['size']! as int,
    );
    expect(sign(path, k), want);
    final p = path.startsWith('/') ? path : '/$path';
    expect(signImgproxyPath(path, k), '/$want$p');
    final raw = SigningKey.fromBytes(
      _hex(v['key']! as String),
      _hex(v['salt']! as String),
      size: v['size']! as int,
    );
    expect(sign(path, raw), want);
  });

  vectorGroup('xi_sig', (v) {
    final k = SigningKey(
      key: v['key']! as String,
      salt: v['salt']! as String,
      size: v['size']! as int,
    );
    final path = v['path']! as String;
    final signed = v['signed_query']! as String;
    expect(signXiQuery(path, v['query']! as String, k), signed);
    expect(signXiQuery(path, signed, k), signed);
    expect(
      signXiUrl('https://img.example.com$path?${v['query']}', k),
      'https://img.example.com$path?$signed',
    );
  });

  vectorGroup('encryption', (v) {
    final token = encryptSource(
      v['plaintext']! as String,
      EncryptionKey(key: v['key']! as String, iv: v['iv']! as String),
    );
    expect(token, v['token']);
    expect(decrypt(token, v['key']! as String), v['plaintext']);
  });

  vectorGroup('pdf_password', (v) {
    final token = encryptPdfPassword(
      v['password']! as String,
      EncryptionKey(key: v['key']! as String, iv: v['iv']! as String),
    );
    expect(token, v['token']);
  });

  test('a random IV makes different tokens', () {
    const key =
        '1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1';
    final k = EncryptionKey(key: key);
    final a = encryptPdfPassword('s3cret', k);
    final b = encryptPdfPassword('s3cret', k);
    expect(a, isNot(b));
    expect(decrypt(a, key), 's3cret');
    expect(k.hasFixedIv, isFalse);
  });

  test('xiMessage', () {
    expect(xiMessage('/', 'url=x&&xi_sig=s&w=1'), (
      signature: 's',
      message: '/?url=x&w=1',
    ));
    expect(xiMessage('/', ''), (signature: null, message: '/'));
    expect(
      () => xiMessage('/', 'xi_sig=a&xi_sig=b'),
      throwsA(
        isA<UrlBuildException>()
            .having((e) => e.code, 'code', ErrorCode.invalidRequest),
      ),
    );
  });

  test('keys hide their material', () {
    final s = SigningKey(key: '00' * 32, salt: '11' * 32);
    expect(s.toString(), isNot(contains('00')));
    final e = EncryptionKey(key: 'ab' * 32);
    expect(e.toString(), isNot(contains('ab')));
  });
}
