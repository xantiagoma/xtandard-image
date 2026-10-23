// The HTTP client against a stand-in server (package:http's MockClient).

import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:test/test.dart';
import 'package:xtandard_image/xtandard_image.dart';

final key = SigningKey(key: '736563726574', salt: '68656C6C6F');
const base = 'http://xi.test';
final body = List<int>.generate(1000, (i) => i % 256);
final videoUrl = ImgproxyUrlBuilder(baseUrl: base, signing: key)
    .videoUrl('s3://m/clip.mov', options: {'vd': 2}, extension: 'webm');

http.Response _json(int status, Object body) => http.Response(
      jsonEncode(body),
      status,
      headers: {'content-type': 'application/json'},
    );

/// The stand-in server; [seen] records every request.
MockClient standIn(List<http.BaseRequest> seen) => MockClient((req) async {
      seen.add(req);
      final path = req.url.path;
      final head = req.method == 'HEAD';
      if (path == '/healthz') return http.Response('ok\n', 200);
      if (path == '/readyz') return http.Response('draining\n', 503);
      if (path.startsWith('/info/')) {
        return _json(200, {
          'width': 640,
          'height': 480,
          'format': 'jpeg',
          'dominant_colors': {
            'vibrant': {'R': 1, 'G': 2, 'B': 3},
          },
          'objects': [
            {
              'class_id': 0,
              'class_name': 'face',
              'confidence': 0.9,
              'left': 0.1,
              'top': 0.2,
              'width': 0.3,
              'height': 0.4,
              'landmarks': [
                [0.1, 0.2],
              ],
            },
          ],
          'exif': {'Make': 'Canon'},
          'future_field': 1,
        });
      }
      if (path.startsWith('/video/') && path.endsWith('.m3u8')) {
        return http.Response(
          '#EXTM3U\n/_stream/T/v0.m3u8\n',
          200,
          headers: {'content-type': 'application/vnd.apple.mpegurl'},
        );
      }
      if (path.startsWith('/video/') || path.startsWith('/_stream/')) {
        if (path.startsWith('/video/') && req.url.toString() != videoUrl) {
          return _json(403, {
            'error': {'kind': 'unauthorized', 'message': 'invalid signature'},
          });
        }
        final m = RegExp(r'^bytes=(\d+)-(\d*)$')
            .firstMatch(req.headers['range'] ?? '');
        if (m != null) {
          final start = int.parse(m[1]!);
          final end = m[2]!.isEmpty ? body.length - 1 : int.parse(m[2]!);
          return http.Response.bytes(
            head ? [] : body.sublist(start, end + 1),
            206,
            headers: {
              'content-type': 'video/webm',
              'content-range': 'bytes $start-$end/${body.length}',
            },
          );
        }
        return http.Response.bytes(
          head ? [] : body,
          200,
          headers: {'content-type': 'video/webm'},
        );
      }
      if (path == '/_admin/play/api/meta') {
        return _json(200, {'version': 'test'});
      }
      if (path == '/proxy-error') {
        return http.Response('<html>bad gateway</html>', 502);
      }
      if (path == '/busy') {
        return _json(429, {
          'error': {
            'kind': 'overloaded',
            'message': 'too many requests',
            'request_id': 'r1',
          },
        });
      }
      if (path == '/down') throw http.ClientException('refused', req.url);
      if (path == '/slow') {
        await Future<void>.delayed(const Duration(seconds: 2));
        return http.Response('late', 200);
      }
      return _json(404, {
        'error': {'kind': 'not_found', 'message': 'source image not found'},
      });
    });

void main() {
  late List<http.BaseRequest> seen;
  late XtandardClient xi;

  setUp(() {
    seen = [];
    xi = XtandardClient(
      base,
      signing: key,
      httpClient: standIn(seen),
      playgroundPath: '/_admin/play/',
      headers: {'x-app': 'test'},
    );
  });

  test('health', () async {
    await xi.healthz();
    final r = await xi.readyz();
    expect(r, (ready: false, state: ReadyState.draining));
    expect(seen.last.headers['x-app'], 'test');
  });

  test('info is typed', () async {
    final info = await xi
        .info('s3://b/cat.jpg', options: {'size': true, 'format': true});
    expect(info.width, 640);
    expect(info.format, 'jpeg');
    expect(info.dominantColors?.vibrant?.g, 2);
    expect(info.objects?.single.className, 'face');
    expect(info.objects?.single.landmarks?.single, [0.1, 0.2]);
    expect(info.exif, {'Make': 'Canon'});
    expect(info.json['future_field'], 1);
    expect(seen.last.headers['accept'], 'application/json');
    expect(seen.last.url.path, startsWith('/info/'));
    final again = await xi.infoAt(seen.last.url.path);
    expect(again.height, 480);
  });

  test('video: whole, range and HEAD', () async {
    var res = await xi.video('s3://m/clip.mov',
        options: {'vd': 2}, extension: 'webm');
    expect(res.bodyBytes, body);
    res = await xi.video(
      's3://m/clip.mov',
      options: {'vd': 2},
      extension: 'webm',
      range: const ByteRange(10, 19),
    );
    expect(res.statusCode, 206);
    expect(res.headers['content-range'], 'bytes 10-19/1000');
    expect(res.bodyBytes, body.sublist(10, 20));
    expect(seen.last.headers['range'], 'bytes=10-19');
    res = await xi.video(
      's3://m/clip.mov',
      options: {'vd': 2},
      extension: 'webm',
      method: HttpMethod.head,
    );
    expect(seen.last.method, 'HEAD');
    expect(res.headers['content-type'], 'video/webm');
  });

  test('video: streamed body', () async {
    final res = await xi.videoStreamed(
      's3://m/clip.mov',
      options: {'vd': 2},
      extension: 'webm',
    );
    final bytes = await res.stream.toBytes();
    expect(bytes, body);
  });

  test('a bad signature is forbidden', () async {
    await expectLater(
      xi.video('s3://m/clip.mov', options: {'vd': 3}, extension: 'webm'),
      throwsA(
        isA<ForbiddenException>()
            .having((e) => e.status, 'status', 403)
            .having((e) => e.kind, 'kind', 'unauthorized')
            .having((e) => e.message, 'message', 'invalid signature')
            .having((e) => e.errorKind, 'errorKind', ErrorKind.unauthorized),
      ),
    );
  });

  test('stream and a ticket child', () async {
    final m = await xi.stream(
      's3://m/clip.mov',
      protocol: StreamProtocol.hls,
      renditions: [360, 720],
    );
    expect(m.url, contains('/st:hls:360:720/'));
    expect(m.contentType, 'application/vnd.apple.mpegurl');
    expect(seen.last.headers['accept'],
        startsWith('application/vnd.apple.mpegurl'));
    final child = const LineSplitter().convert(m.body)[1];
    final res = await xi.media(child, range: const ByteRange.suffix(4));
    expect(seen.last.url.path, '/_stream/T/v0.m3u8');
    expect(seen.last.headers['range'], 'bytes=-4');
    expect(res.statusCode, 200); // the stand-in only answers start-end ranges
  });

  test('errors by status', () async {
    await expectLater(
      xi.image('s3://b/missing.jpg'),
      throwsA(
          isA<NotFoundException>().having((e) => e.kind, 'kind', 'not_found')),
    );
    await expectLater(
      xi.media('/proxy-error'),
      throwsA(
        isA<GatewayException>()
            .having((e) => e.kind, 'kind', 'unknown')
            .having((e) => e.message, 'message', '<html>bad gateway</html>')
            .having((e) => e.body, 'body', isNull),
      ),
    );
    await expectLater(
      xi.media('/busy'),
      throwsA(
        isA<TooManyRequestsException>()
            .having((e) => e.requestId, 'requestId', 'r1'),
      ),
    );
    await expectLater(xi.media('/down'), throwsA(isA<TransportException>()));
    expect(
      XtandardHttpException.forStatus(503, 'u', 'm'),
      isA<ServiceUnavailableException>(),
    );
    expect(
        XtandardHttpException.forStatus(500, 'u', 'm'), isA<ServerException>());
    expect(XtandardHttpException.forStatus(418, 'u', 'm').runtimeType,
        XtandardHttpException);
  });

  test('timeouts and aborts are transport errors', () async {
    final slow = XtandardClient(
      base,
      httpClient: standIn(seen),
      timeout: const Duration(milliseconds: 20),
    );
    await expectLater(slow.media('/slow'), throwsA(isA<TransportException>()));
    // MockClient hands its handler a copy of the request; the streaming
    // variant sees the original, abortable one.
    final abortable = XtandardClient(
      base,
      httpClient: MockClient.streaming((req, _) async {
        if (req case http.Abortable(:final abortTrigger?)) {
          await abortTrigger;
          throw http.RequestAbortedException(req.url);
        }
        return http.StreamedResponse(const Stream.empty(), 200);
      }),
    );
    final abort = Completer<void>();
    final pending = abortable.media('/abortable', abortTrigger: abort.future);
    abort.complete();
    await expectLater(pending, throwsA(isA<TransportException>()));
  });

  test('playground meta', () async {
    expect(await xi.playgroundMeta(), {'version': 'test'});
  });

  test('range headers', () {
    expect(rangeHeader(const ByteRange(0)), 'bytes=0-');
    expect(rangeHeader(const ByteRange(5, 9)), 'bytes=5-9');
    expect(rangeHeader(const ByteRange.suffix(100)), 'bytes=-100');
    expect(rangeHeader(const ByteRange.raw('bytes=1-2')), 'bytes=1-2');
    for (final bad in const [
      ByteRange(5, 4),
      ByteRange(-1),
      ByteRange.suffix(-1)
    ]) {
      expect(
        () => rangeHeader(bad),
        throwsA(isA<UrlBuildException>()
            .having((e) => e.code, 'code', ErrorCode.invalidRange)),
      );
    }
  });

  test('an owned client closes', () {
    final c = XtandardClient('$base/')..close();
    expect(c.baseUrl, base);
    expect(c.urls.baseUrl, base);
  });

  test('the error envelope is parsed', () {
    final err = toXtandardException(
      http.Response(
        jsonEncode({
          'error': {'kind': 'invalid_url', 'message': 'invalid url'},
        }),
        400,
      ),
      '$base/x',
    );
    expect(err, isA<BadRequestException>());
    expect((err.kind, err.message, err.url),
        ('invalid_url', 'invalid url', '$base/x'));
  });
}
