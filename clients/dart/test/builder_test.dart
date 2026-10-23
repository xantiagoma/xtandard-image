// Unit tests of the builders beyond the shared fixtures: the typed
// per-option methods and option values, number formatting, the map form's
// bare values, and error codes.

import 'package:test/test.dart';
import 'package:xtandard_image/xtandard_image.dart';

Matcher throwsCode(ErrorCode code) =>
    throwsA(isA<UrlBuildException>().having((e) => e.code, 'code', code));

const src = 'https://example.com/images/cat.jpg';
const b64 = 'aHR0cHM6Ly9leGFtcGxlLmNvbS9pbWFnZXMvY2F0LmpwZw';

void main() {
  const img = ImgproxyUrlBuilder();

  group('typed per-option methods', () {
    test('write the name as called and trim left-out optional arguments', () {
      expect(
        img.image(src).resize('fill', 300, 400).q(80).extension('webp').build(),
        '/insecure/rs:fill:300:400/q:80/$b64.webp',
      );
      expect(
        img.image(src).rs('fill', 300, null, true).build(),
        '/insecure/rs:fill:300::1/$b64',
      );
      expect(img.image(src).autoRotate(null).build(), '/insecure/ar:/$b64');
    });

    test('repeated groups are records', () {
      expect(
        img.image(src).ba(2, [(left: 0, top: 0, width: 0.5, height: 0.5)]).fq([
          (format: 'webp', quality: 70),
          (format: 'avif', quality: 50)
        ]).ct(['a', 'b']).build(),
        '/insecure/ba:2:0:0:0.5:0.5/fq:webp:70:avif:50/ct:a:b/$b64',
      );
    });

    test('options with several forms take every form', () {
      expect(img.image(src).g('sm').build(), '/insecure/g:sm/$b64');
      expect(img.image(src).g('fp', 0.5, 0.25).build(),
          '/insecure/g:fp:0.5:0.25/$b64');
      expect(
        img.image(src).gravity('obj', 'face', 'cat', ['dog']).build(),
        '/insecure/g:obj:face:cat:dog/$b64',
      );
      expect(img.image(src).bg(255, 0, 0).build(), '/insecure/bg:255:0:0/$b64');
      expect(img.image(src).bg('ff0000').build(), '/insecure/bg:ff0000/$b64');
    });

    test('reserved words get a trailing underscore', () {
      expect(
        img.info(src).do_(true).build(),
        '/info/insecure/do:1/$b64',
      );
      expect(
        img.video(src).fade(1, 2).extension('mp4').build(),
        '/video/insecure/fd:1:2/$b64.mp4',
      );
    });

    test('chained pipelines, PDF passwords and encodings', () {
      expect(
        img
            .image(src)
            .w(300)
            .pipeline()
            .bl(2)
            .encoding(SourceEncoding.plain)
            .build(),
        '/insecure/w:300/-/bl:2/plain/https://example.com/images/cat.jpg',
      );
      expect(
        () => img.image(src).withPdfPassword('x').build(),
        throwsCode(ErrorCode.missingEncryption),
      );
    });

    test('canonical names', () {
      const canonical = ImgproxyUrlBuilder(optionNames: OptionNames.canonical);
      expect(
        canonical.image(src).rs('fit', 10, 10).q(5).build(),
        '/insecure/resize:fit:10:10/quality:5/$b64',
      );
    });
  });

  group('typed option values', () {
    test('are added in order and compare by value', () {
      final opts = [
        ProcessingOption.resize('fill', 300, 400),
        ProcessingOption.quality(80),
        ProcessingOption.blurAreas(1, [(left: 0, top: 0, width: 1, height: 1)]),
      ];
      expect(
        img.image(src).addAll(opts).build(),
        '/insecure/rs:fill:300:400/q:80/ba:1:0:0:1:1/$b64',
      );
      expect(ProcessingOption.quality(80), ProcessingOption.quality(80));
      expect(ProcessingOption.quality(80).hashCode,
          ProcessingOption.quality(80).hashCode);
      expect(ProcessingOption.quality(80), isNot(ProcessingOption.quality(81)));
      expect(ProcessingOption.resize('fit', 1, 2, true).toString(),
          'resize:fit:1:2:1');
      expect(InfoOption.blurhash(4, 3).toString(), 'blurhash:4:3');
      expect(img.info(src).add(InfoOption.size(true)).build(),
          '/info/insecure/s:1/$b64');
      expect(
        img.video(src).add(VideoOption.start(1.5)).extension('webm').build(),
        '/video/insecure/vs:1.5/$b64.webm',
      );
    });

    test('a switch over the sealed hierarchy is exhaustive', () {
      String kind(OptionValue o) => switch (o) {
            ProcessingOption() => 'processing',
            InfoOption() => 'info',
            VideoOption() => 'video',
          };
      expect(kind(VideoOption.mute(true)), 'video');
    });
  });

  group('map form', () {
    test('bare values, nulls and lists', () {
      expect(
        img.url(src, options: {
          'w': 300,
          'h': null,
          'rs': ['fit', 10, 10],
          'sm': true
        }),
        '/insecure/w:300/rs:fit:10:10/sm:1/$b64',
      );
      expect(
        img.url(src, options: {
          'w': 300
        }, pipelines: [
          {},
          {'bl': 2}
        ]),
        '/insecure/w:300/-/bl:2/$b64',
      );
    });

    test('unsupported argument types', () {
      expect(
        () => img.url(src, options: {'w': DateTime(2026)}),
        throwsCode(ErrorCode.invalidArgument),
      );
      expect(
        () => img.image(src).option('w', [<int>[]]).build(),
        throwsCode(ErrorCode.invalidArgument),
      );
      expect(
        () => img.url(src, options: {'w': double.nan}),
        throwsCode(ErrorCode.invalidArgument),
      );
      expect(
        () => img.url(src, options: {'w': double.infinity}),
        throwsCode(ErrorCode.invalidArgument),
      );
    });
  });

  test('numbers are written as ECMAScript writes them', () {
    final cases = <num, String>{
      300: '300',
      300.0: '300',
      -0.0: '0',
      1.5: '1.5',
      0.1: '0.1',
      -2.25: '-2.25',
      1e-7: '1e-7',
      0.000001: '0.000001',
      1e21: '1e+21',
      1.5e300: '1.5e+300',
      123456789012345680000.0: '123456789012345680000',
      0.30000000000000004: '0.30000000000000004',
    };
    cases
        .forEach((n, want) => expect(formatNumber(n, 'n'), want, reason: '$n'));
  });

  test('escapes', () {
    expect(escapePlainSource('a b/é?#%'), 'a%20b/%C3%A9%3F%23%25');
    expect(escapeQueryValue('a&b=c d'), 'a%26b%3Dc%20d');
    // A lone surrogate is written as U+FFFD, like TextEncoder.
    expect(escapePlainSource(String.fromCharCode(0xd800)), '%EF%BF%BD');
  });

  test('enums parse their wire values', () {
    expect(StreamProtocol.fromValue('hls'), StreamProtocol.hls);
    expect(StreamProtocol.hls.contentType, 'application/vnd.apple.mpegurl');
    expect(SegmentFormat.fromValue('mpegts'), SegmentFormat.ts);
    expect(SegmentFormat.defaultFormat, SegmentFormat.fmp4);
    expect(SourceScheme.fromValue('s3'), SourceScheme.s3);
    expect(ErrorKind.fromValue('not_found')?.status, 404);
    expect(processingNames['rs'], 'resize');
    expect(processingShortNames['resize'], 'rs');
  });

  test('query builder', () {
    const q = QueryUrlBuilder(baseUrl: 'https://img.example.com/');
    expect(
      q.url('s3://b/cat.jpg',
          params: {'w': 300, 'f': 'webp'}, options: {'bl': 2}),
      'https://img.example.com/q/?url=s3://b/cat.jpg&w=300&f=webp&o=bl:2',
    );
    expect(
      q
          .image('s3://b/cat.jpg')
          .param('w', 300)
          .blur(2)
          .extension('png')
          .build(),
      'https://img.example.com/q/?url=s3://b/cat.jpg&w=300&o=bl:2&format=png',
    );
    expect(
      q.video('s3://m/a.mov').crop(100, 50).vs(1).build(),
      'https://img.example.com/q/video?url=s3://m/a.mov&c=100,50&vs=1',
    );
    expect(
      q.streamUrl('s3://m/a.mov',
          protocol: StreamProtocol.dash, renditions: [360]),
      'https://img.example.com/q/video?url=s3://m/a.mov&st=dash,360&format=mpd',
    );
    expect(
      q.imaginaryUrl('resize', 's3://b/cat.jpg', params: {'width': 300}),
      'https://img.example.com/q/resize?url=s3://b/cat.jpg&width=300',
    );
  });

  test('key errors', () {
    expect(() => SigningKey(key: 'zz', salt: '00'),
        throwsCode(ErrorCode.invalidKey));
    expect(
      () => SigningKey(key: '00', salt: '00', size: 33),
      throwsCode(ErrorCode.invalidKey),
    );
    expect(() => EncryptionKey(key: '00'), throwsCode(ErrorCode.invalidKey));
    expect(
      () => EncryptionKey(key: '00' * 32, iv: '00'),
      throwsCode(ErrorCode.invalidKey),
    );
  });
}
