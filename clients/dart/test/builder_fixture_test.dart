// testdata/urls/imgproxy-builder.json: 400+ cases derived from the
// server's golden tables.
//
// The URLs were written by the TypeScript client and parsed back by the Go
// dialect tests (TestBuilderFixture); every client must write the same
// ones: `url` with option names as given, `url_short` with the default
// short names, and `query_url` (query dialect) where the case has one.

import 'package:test/test.dart';
import 'package:xtandard_image/xtandard_image.dart';

import 'support/fixtures.dart';

void main() {
  fixtureTests(
    'testdata/urls/imgproxy-builder.json',
    cases: (f) => (f['cases']! as List).cast<Json>(),
    name: (c) => c['name']! as String,
    run: (fixture, c) {
      expect(_imgproxy(fixture, c, given: true), c['url'], reason: 'url');
      expect(
        _imgproxy(fixture, c, given: false),
        c['url_short'],
        reason: 'url_short',
      );
      if (c['query'] == true) {
        expect(_query(fixture, c), c['query_url'], reason: 'query_url');
      }
    },
  );
}

SigningKey? _signing(Json fixture, Json c) {
  final size = c['signature_size'] as int?;
  if (size == null || size == 0) return null;
  final k = fixture['signing']! as Json;
  return SigningKey(
    key: k['key']! as String,
    salt: k['salt']! as String,
    size: size,
  );
}

void _apply<T extends UrlChain<T, O>, O extends OptionValue>(
  T chain,
  Json c,
  void Function()? next,
) {
  final pipelines = c['pipelines']! as List;
  for (var i = 0; i < pipelines.length; i++) {
    if (i > 0) next!();
    for (final entry in pipelines[i] as List) {
      for (final MapEntry(:key, :value) in (entry as Json).entries) {
        chain.option(key, (value! as List).cast<Object?>());
      }
    }
  }
}

String _imgproxy(Json fixture, Json c, {required bool given}) {
  final enc = fixture['encryption']! as Json;
  final b = ImgproxyUrlBuilder(
    unsignedSignature: 'unsafe',
    encryption: EncryptionKey(
      key: enc['key']! as String,
      iv: enc['iv']! as String,
    ),
    signing: _signing(fixture, c),
    separator: (c['separator'] as String?) ?? ':',
    optionNames: given ? OptionNames.given : OptionNames.short,
  );
  final source = c['source']! as String;
  final encoding = SourceEncoding.fromValue(c['encoding']! as String)!;
  final ext = c['extension'] as String?;
  switch (c['endpoint']) {
    case 'info':
      final ch = b.info(source).encoding(encoding);
      _apply(ch, c, null);
      return ch.build();
    case 'video':
      final ch = b.video(source).encoding(encoding);
      _apply(ch, c, null);
      if (ext != null && ext.isNotEmpty) ch.extension(ext);
      return ch.build();
    default:
      final ch = b.image(source).encoding(encoding);
      _apply(ch, c, ch.pipeline);
      if (ext != null && ext.isNotEmpty) ch.extension(ext);
      return ch.build();
  }
}

String _query(Json fixture, Json c) {
  final q = QueryUrlBuilder(signing: _signing(fixture, c));
  final source = c['source']! as String;
  final ext = c['extension'] as String?;
  if (c['endpoint'] == 'video') {
    final ch = q.video(source);
    _apply(ch, c, null);
    if (ext != null && ext.isNotEmpty) ch.extension(ext);
    return ch.build();
  }
  final ch = q.image(source);
  _apply(ch, c, ch.pipeline);
  if (ext != null && ext.isNotEmpty) ch.extension(ext);
  return ch.build();
}
