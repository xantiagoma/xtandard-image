// Smoke test against a running server, skipped unless XI_SMOKE_URL is set.
//
// The playground demo (compose.playground.yml, unsigned URLs allowed)
// works:
//
//   XI_SMOKE_URL=http://127.0.0.1:8080 dart test test/smoke_test.dart
//
// It needs the demo's local fixtures (xi://local/…) and, for the /video
// cases, a full or gpl image with XI_VIDEO_TRANSFORM_ENABLED.
@TestOn('vm')
library;

import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;
import 'package:test/test.dart';
import 'package:xtandard_image/xtandard_image.dart';

final String base = Platform.environment['XI_SMOKE_URL'] ?? '';
final String image = xiSource('local', 'jpeg/baseline.jpg');
final String video = xiSource('local', 'video/bbb-h264-aac.mp4');

void main() {
  final skip = base.isEmpty ? 'set XI_SMOKE_URL to run against a server' : null;

  group('smoke', skip: skip, () {
    late XtandardClient xi;
    setUp(
        () => xi = XtandardClient(base, sourceEncoding: SourceEncoding.plain));
    tearDown(() => xi.close());

    test('health, image and info', () async {
      await xi.healthz();
      expect((await xi.readyz()).ready, isTrue);
      final res = await xi.image(
        image,
        options: {
          'rs': ['fit', 64, 64]
        },
        extension: 'webp',
      );
      expect(res.headers['content-type'], 'image/webp');
      final info = await xi.info(
        image,
        options: {'size': true, 'format': true, 'dimensions': true},
      );
      expect(info.format, 'jpeg');
      expect(info.width, greaterThan(0));
      await expectLater(
        xi.image(xiSource('local', 'jpeg/missing.jpg')),
        throwsA(isA<NotFoundException>()),
      );
    });

    test('fluent URL fetched with the same client', () async {
      final url =
          xi.urls.image(image).resize('fill', 32, 32).format('png').build();
      final res = await xi.media(url);
      expect(res.headers['content-type'], 'image/png');
    });

    test('query dialect', () async {
      final url = QueryUrlBuilder(baseUrl: base)
          .url(image, params: {'w': 32, 'f': 'png'});
      expect((await xi.media(url)).headers['content-type'], 'image/png');
    });

    test('video range, HEAD and stream', () async {
      final res = await xi.video(
        video,
        options: {'vd': 1, 'w': 160},
        extension: 'webm',
        range: const ByteRange(0, 99),
      );
      expect(res.statusCode, anyOf(200, 206));
      final head = await xi.video(
        video,
        options: {'vd': 1, 'w': 160},
        extension: 'webm',
        method: HttpMethod.head,
      );
      expect(head.headers['content-type'], startsWith('video/webm'));
      final m = await xi
          .stream(video, protocol: StreamProtocol.hls, renditions: [240]);
      expect(m.body, startsWith('#EXTM3U'));
      final child = const LineSplitter()
          .convert(m.body)
          .firstWhere((l) => l.isNotEmpty && !l.startsWith('#'));
      final part = await xi.mediaStreamed(child);
      expect(part.statusCode, 200);
      await part.stream.drain<void>();
    });

    test('a custom http.Client', () async {
      final inner = http.Client();
      final c = XtandardClient(base, httpClient: inner);
      await c.healthz();
      c.close();
      inner.close();
    });
  });
}
