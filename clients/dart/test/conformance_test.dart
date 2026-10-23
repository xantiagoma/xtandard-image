// clients/spec/conformance.json: the cases every official client runs
// (ADR 0013).
//
// Positive cases must produce expect.value byte for byte; error cases must
// throw UrlBuildException with expect.error as its code. The fixture's
// options are pipelines of [name, args] pairs; they are built twice, as
// option maps (url()) and through the fluent chains (option()), and both
// must agree.

import 'package:test/test.dart';
import 'package:xtandard_image/xtandard_image.dart';

import 'support/fixtures.dart';

/// Cases the statically typed API cannot express, by name.
const Set<String> notExpressible = {
  // infoUrl() and videoUrl() take one options map, and the /info and
  // /video chains have no pipeline(): chained pipelines cannot be written.
  'err_info_chained',
  'err_video_chained',
  // videoUrl() and the /video chain have no PDF password parameter.
  'err_video_pdf_password',
  // The protocol is the StreamProtocol enum: an unknown one does not
  // compile.
  'err_stream_unknown_protocol',
};

void main() {
  fixtureTests(
    'clients/spec/conformance.json',
    cases: (f) => (f['cases']! as List).cast<Json>(),
    name: (c) => c['name']! as String,
    skipped: notExpressible,
    run: (fixture, c) => runCase(fixture['keys']! as Json, c),
  );
}

void runCase(Json keys, Json c) {
  final expected = c['expect']! as Json;
  final error = expected['error'] as String?;
  if (error != null) {
    try {
      final url = runMapping(keys, c);
      fail('map form built $url, want error $error');
    } on UrlBuildException catch (e) {
      expect(e.code.value, error, reason: 'map form');
    }
    String? fluent;
    try {
      fluent = runFluent(keys, c);
    } on UrlBuildException catch (e) {
      expect(e.code.value, error, reason: 'fluent form');
      return;
    } on NotExpressible {
      return;
    }
    expect(fluent, isNull, reason: 'fluent form built $fluent');
    return;
  }
  final value = expected['value']! as String;
  expect(runMapping(keys, c), value, reason: 'map form');
  try {
    final fluent = runFluent(keys, c);
    if (fluent != null) expect(fluent, value, reason: 'fluent form');
  } on NotExpressible {
    // the map form covered it
  }
}

SigningKey? _signing(Json keys, Json cfg) {
  final s = cfg['signing'];
  if (s == null) return null;
  final k = s == true ? keys['signing']! as Json : s as Json;
  return SigningKey(
    key: k['key']! as String,
    salt: k['salt']! as String,
    size: (cfg['signature_size'] as int?) ?? defaultSignatureSize,
  );
}

EncryptionKey? _encryption(Json keys, Json cfg) {
  final e = cfg['encryption'];
  if (e == null) return null;
  final k = e == true ? keys['encryption']! as Json : e as Json;
  return EncryptionKey(key: k['key']! as String, iv: k['iv'] as String?);
}

SourceEncoding? _encoding(Object? v) =>
    v == null ? null : SourceEncoding.fromValue(v as String)!;

ImgproxyUrlBuilder _imgproxy(Json keys, Json cfg) => ImgproxyUrlBuilder(
      baseUrl: (cfg['base_url'] as String?) ?? '',
      signing: _signing(keys, cfg),
      unsignedSignature:
          (cfg['unsigned_signature'] as String?) ?? defaultUnsigned,
      encryption: _encryption(keys, cfg),
      sourceEncoding:
          _encoding(cfg['source_encoding']) ?? SourceEncoding.base64,
      splitSource: cfg['split_source'] as int?,
      separator: (cfg['separator'] as String?) ?? defaultSeparator,
      optionNames: OptionNames.values
          .byName((cfg['option_names'] as String?) ?? 'short'),
    );

QueryUrlBuilder _query(Json keys, Json cfg) => QueryUrlBuilder(
      baseUrl: (cfg['base_url'] as String?) ?? '',
      mount: (cfg['mount'] as String?) ?? queryMount,
      signing: _signing(keys, cfg),
      encryption: _encryption(keys, cfg),
      separator: (cfg['separator'] as String?) ?? defaultSeparator,
      optionNames: OptionNames.values
          .byName((cfg['option_names'] as String?) ?? 'short'),
    );

/// The pipelines of a case: lists of [name, args].
List<List<(String, List<Object?>)>> _pipelines(Json inp) => [
      for (final p in (inp['options'] as List?) ?? const [])
        [
          for (final e in p as List)
            ((e as List)[0] as String, (e[1] as List).cast<Object?>()),
        ],
    ];

List<Map<String, Object?>>? _maps(Json inp) {
  if (inp['options'] == null) return null;
  return [
    for (final p in _pipelines(inp)) {for (final (n, a) in p) n: a},
  ];
}

/// The first pipeline as `options` and the others as `pipelines`.
(Map<String, Object?>?, List<Map<String, Object?>>?) _split(Json inp) {
  final m = _maps(inp);
  if (m == null || m.isEmpty) return (null, null);
  return (m.first, m.length > 1 ? m.sublist(1) : null);
}

/// The one options map of /info, /video and streams.
Map<String, Object?>? _one(Json inp, String what) {
  final m = _maps(inp);
  if (m == null || m.isEmpty) return null;
  if (m.length > 1) throw NotExpressible('$what takes one options map');
  return m.first;
}

StreamProtocol _protocol(Json inp) =>
    StreamProtocol.fromValue(inp['protocol']! as String) ??
    (throw const NotExpressible('the protocol is the StreamProtocol enum'));

SegmentFormat? _segmentFormat(Json inp) {
  final v = inp['segment_format'] as String?;
  if (v == null) return null;
  return SegmentFormat.fromValue(v) ??
      (throw const NotExpressible(
          'the segment format is the SegmentFormat enum'));
}

Map<String, Object?>? _params(Json inp) {
  final p = inp['params'] as List?;
  if (p == null) return null;
  return {for (final e in p) (e as List)[0] as String: e[1]};
}

ByteRange _range(Object? r) => switch (r) {
      final String s => ByteRange.raw(s),
      {'suffix': final int n} => ByteRange.suffix(n),
      {'start': final int start} => ByteRange(start, (r as Map)['end'] as int?),
      _ => throw ArgumentError('bad range $r'),
    };

/// The case through the object form (option maps).
String runMapping(Json keys, Json c) {
  final cfg = (c['config'] as Json?) ?? const {};
  final inp = c['input']! as Json;
  final source = inp['source'] as String?;
  switch (c['op']) {
    case 'query.query_request':
      // input.build names the query op that builds the URL from the other
      // inputs.
      final q = queryMethodRequest(
        runMapping(keys, {...c, 'op': inp['build']}),
      );
      return '${q.method} ${q.url}\n'
          'Content-Type: ${q.contentType}\n\n${q.body}';
    case 'imgproxy.url':
      final (options, pipelines) = _split(inp);
      return _imgproxy(keys, cfg).url(
        source!,
        options: options,
        pipelines: pipelines,
        extension: inp['extension'] as String?,
        sourceEncoding: _encoding(inp['source_encoding']),
        pdfPassword: inp['pdf_password'] as String?,
      );
    case 'imgproxy.info_url':
      return _imgproxy(keys, cfg).infoUrl(
        source!,
        options: _one(inp, 'infoUrl()'),
        sourceEncoding: _encoding(inp['source_encoding']),
        pdfPassword: inp['pdf_password'] as String?,
      );
    case 'imgproxy.video_url':
      if (inp.containsKey('pdf_password')) {
        throw const NotExpressible('videoUrl() has no pdfPassword parameter');
      }
      return _imgproxy(keys, cfg).videoUrl(
        source!,
        options: _one(inp, 'videoUrl()'),
        extension: inp['extension'] as String?,
        sourceEncoding: _encoding(inp['source_encoding']),
      );
    case 'imgproxy.stream_url':
      return _imgproxy(keys, cfg).streamUrl(
        source!,
        protocol: _protocol(inp),
        renditions: (inp['renditions'] as List?)?.cast<int>(),
        segmentDuration: inp['segment_duration'] as int?,
        segmentFormat: _segmentFormat(inp),
        options: _one(inp, 'streamUrl()'),
        sourceEncoding: _encoding(inp['source_encoding']),
      );
    case 'query.url':
      final (options, pipelines) = _split(inp);
      return _query(keys, cfg).url(
        source!,
        params: _params(inp),
        options: options,
        pipelines: pipelines,
        extension: inp['extension'] as String?,
        pdfPassword: inp['pdf_password'] as String?,
      );
    case 'query.video_url':
      final (options, pipelines) = _split(inp);
      return _query(keys, cfg).videoUrl(
        source!,
        options: options,
        pipelines: pipelines,
        extension: inp['extension'] as String?,
      );
    case 'query.stream_url':
      return _query(keys, cfg).streamUrl(
        source!,
        protocol: _protocol(inp),
        renditions: (inp['renditions'] as List?)?.cast<int>(),
        segmentDuration: inp['segment_duration'] as int?,
        segmentFormat: _segmentFormat(inp),
        options: _one(inp, 'streamUrl()'),
      );
    case 'query.imaginary_url':
      return _query(keys, cfg).imaginaryUrl(
        inp['endpoint']! as String,
        source!,
        params: _params(inp),
      );
    case 'sign':
      return sign(inp['message']! as String, _signing(keys, cfg)!);
    case 'sign_xi_url':
      return signXiUrl(
        inp['url']! as String,
        _signing(keys, cfg)!,
        mount: (inp['mount'] as String?) ?? '',
      );
    case 'encrypt_source':
      return encryptSource(source!, _encryption(keys, cfg)!);
    case 'encrypt_pdf_password':
      return encryptPdfPassword(
        inp['password']! as String,
        _encryption(keys, cfg)!,
      );
    case 'xi_source':
      return xiSource(inp['name']! as String, inp['key']! as String);
    case 'source_url':
      return sourceUrl(
        SourceScheme.fromValue(inp['scheme']! as String)!,
        inp['authority']! as String,
        inp['key']! as String,
      );
    case 'range_header':
      return rangeHeader(_range(inp['range']));
    case 'escape_plain_source':
      return escapePlainSource(inp['value']! as String);
    case 'escape_query_value':
      return escapeQueryValue(inp['value']! as String);
    default:
      throw ArgumentError('unknown op ${c['op']}');
  }
}

/// Appends the case's pipelines to a chain; [next] starts a chained
/// pipeline, or is null where the chain has none.
void _apply<T extends UrlChain<T, O>, O extends OptionValue>(
  T chain,
  Json inp,
  void Function()? next,
) {
  final pipelines = _pipelines(inp);
  for (var i = 0; i < pipelines.length; i++) {
    if (i > 0) {
      if (next == null) {
        throw const NotExpressible('the chain has no pipeline()');
      }
      next();
    }
    for (final (name, args) in pipelines[i]) {
      chain.option(name, args);
    }
  }
}

/// The case through the fluent chains, for the ops that have one.
String? runFluent(Json keys, Json c) {
  final cfg = (c['config'] as Json?) ?? const {};
  final inp = c['input']! as Json;
  final source = inp['source'] as String?;
  final encoding = _encoding(inp['source_encoding']);
  final ext = inp['extension'] as String?;
  final password = inp['pdf_password'] as String?;
  switch (c['op']) {
    case 'imgproxy.url':
      final ch = _imgproxy(keys, cfg).image(source!);
      _apply(ch, inp, ch.pipeline);
      if (encoding != null) ch.encoding(encoding);
      if (ext != null) ch.extension(ext);
      if (password != null) ch.withPdfPassword(password);
      return ch.build();
    case 'imgproxy.info_url':
      final ch = _imgproxy(keys, cfg).info(source!);
      _apply(ch, inp, null);
      if (encoding != null) ch.encoding(encoding);
      if (password != null) ch.withPdfPassword(password);
      return ch.build();
    case 'imgproxy.video_url':
      if (password != null) throw const NotExpressible('no PDF password');
      final ch = _imgproxy(keys, cfg).video(source!);
      _apply(ch, inp, null);
      if (encoding != null) ch.encoding(encoding);
      if (ext != null) ch.extension(ext);
      return ch.build();
    case 'query.url':
      final ch = _query(keys, cfg).image(source!);
      for (final MapEntry(:key, :value) in (_params(inp) ?? const {}).entries) {
        ch.param(key, value);
      }
      _apply(ch, inp, ch.pipeline);
      if (ext != null) ch.extension(ext);
      if (password != null) ch.withPdfPassword(password);
      return ch.build();
    case 'query.video_url':
      // Several option maps are one /q/video key list; the chain writes
      // them in order too.
      final ch = _query(keys, cfg).video(source!);
      for (final p in _pipelines(inp)) {
        for (final (name, args) in p) {
          ch.option(name, args);
        }
      }
      if (ext != null) ch.extension(ext);
      return ch.build();
    default:
      return null;
  }
}
