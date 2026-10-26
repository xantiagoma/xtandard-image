// Builds imgproxy, query dialect and stream URLs, then fetches /info.
//
//   dart run example/example.dart [http://127.0.0.1:8080]
//
// With a server URL (the playground demo allows unsigned URLs) it also
// calls the server.
// ignore_for_file: avoid_print

import 'package:xtandard_image/xtandard_image.dart';

Future<void> main(List<String> args) async {
  // On a server: read the key and salt from the environment
  // (XI_SIGNING_KEYS / XI_SIGNING_SALTS). These are example values.
  final signing = SigningKey(key: '736563726574', salt: '68656C6C6F');
  final img = ImgproxyUrlBuilder(
    baseUrl: 'https://img.example.com',
    signing: signing,
  );

  // Map form: names or aliases to argument lists, or the first argument.
  print(img.url(
    's3://photos/cat.jpg',
    options: {
      'rs': ['fill', 1200, 630],
      'g': 'sm',
      'q': 80,
    },
    extension: 'webp',
  ));

  // Fluent form: one typed method per option name and alias.
  print(img
      .image(xiSource('photos', '2026/cat.jpg'))
      .resize('fit', 300, 300)
      .blur(2)
      .extension('png')
      .build());

  // Typed option values.
  print(img.image('s3://photos/cat.jpg').addAll([
    ProcessingOption.resize('fill', 600, 400),
    ProcessingOption.quality(75),
  ]).build());

  // /video and HLS.
  print(img
      .video('s3://media/talk.mov')
      .start(12)
      .duration(8)
      .extension('mp4')
      .build());
  print(img.streamUrl(
    's3://media/talk.mov',
    protocol: StreamProtocol.hls,
    renditions: [360, 720],
  ));

  // The query dialect, signed with xi_sig.
  final q =
      QueryUrlBuilder(baseUrl: 'https://img.example.com', signing: signing);
  print(q.url('s3://photos/cat.jpg', params: {'w': 300, 'f': 'webp'}));

  if (args.isEmpty) return;
  final xi = XtandardClient(args.first, sourceEncoding: SourceEncoding.plain);
  try {
    final info = await xi.info(
      xiSource('local', 'jpeg/baseline.jpg'),
      options: {'size': true, 'format': true, 'dimensions': true},
    );
    print('${info.format} ${info.width}x${info.height}, ${info.size} bytes');
  } on XtandardHttpException catch (e) {
    print('HTTP ${e.status} ${e.kind}: ${e.message}');
  } finally {
    xi.close();
  }
}
