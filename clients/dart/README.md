![xtandard-image](https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png)

# xtandard-image for Dart and Flutter

The official Dart client of [xtandard-image](https://github.com/xantiagoma/xtandard-image) ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)). It has:

- typed URL builders for the imgproxy dialect (processing, `/info`, `/video`, HLS and DASH) and the query dialect (`/q/`, `/q/video`, imaginary endpoints);
- imgproxy and `xi_sig` signing;
- `enc/` source and PDF password encryption;
- an HTTP client on [`package:http`](https://pub.dev/packages/http), with Range, HEAD, streaming and aborts.

It is pure Dart. It works in Flutter apps (iOS, Android, web, desktop) and on Dart servers (shelf, dart_frog, serverpod). It does not depend on Flutter. It needs Dart 3.5 or later (Flutter 3.24 or later). The tests run on the VM and in Chrome, compiled with dart2js and dart2wasm.

It is not on pub.dev yet. The package `xtandard_image` (free on pub.dev as of 2026-10-22) will follow the server's release tags. pub.dev publishes it from this subdirectory, so there is no mirror repository.

## Install (from the repository, until the first release)

```yaml
dependencies:
  xtandard_image:
    git:
      url: https://github.com/xantiagoma/xtandard-image
      path: clients/dart
```

## Keys: servers and apps

The signing key signs every URL, and the encryption key decrypts every `enc/` source and PDF password. On a server, read them from the environment and use them freely.

In an app, signing and encryption work the same way, but they are optional, and **a key embedded in an app can be extracted** from the binary or the JavaScript bundle. Whoever extracts it can sign any URL. Prefer one of these:

- **Unsigned URLs under the server's `restricted` policy.** The server accepts unsigned URLs only for presets and allowed sizes, and its startup warning lists the allowed widths. The app builds plain unsigned URLs.
- **Backend-signed URLs.** Your backend builds and signs URLs with this package (or any official client), and the app only displays them.

## URL builder

```dart
import 'dart:io';

import 'package:xtandard_image/xtandard_image.dart';

final img = ImgproxyUrlBuilder(
  baseUrl: 'https://img.example.com',
  signing: SigningKey(
    key: Platform.environment['XI_SIGNING_KEY']!,
    salt: Platform.environment['XI_SIGNING_SALT']!,
  ),
  // Only for enc/ sources and PDF passwords.
  encryption: EncryptionKey(key: Platform.environment['XI_ENCRYPTION_KEY']!),
);

// Map form: keys are option names or aliases, values are argument lists or a bare first argument.
img.url('s3://photos/cat.jpg', options: {'rs': ['fill', 1200, 630], 'g': 'sm', 'q': 80}, extension: 'webp');
// https://img.example.com/<signature>/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp

// Fluent form: one typed method per option name and alias.
img.image(xiSource('photos', '2026/cat.jpg')).resize('fit', 300, 300).blur(2).extension('png').build();

// Typed option values (a sealed hierarchy: ProcessingOption, InfoOption, VideoOption).
img.image('s3://photos/cat.jpg').addAll([ProcessingOption.resize('fill', 600, 400), ProcessingOption.quality(75)]).build();

// Chained pipelines (/-/): `pipelines:`, or pipeline() in the fluent form.
img.url('s3://photos/cat.jpg', options: {'rs': ['fit', 500, 500]}, pipelines: [{'bl': 2}]);

// Plain or encrypted sources, a password-protected PDF.
img.url('https://example.com/a b.jpg', options: {'w': 300}, sourceEncoding: SourceEncoding.plain);
img.url('s3://private/cat.jpg', options: {'w': 300}, sourceEncoding: SourceEncoding.encrypted);
img.url('s3://docs/report.pdf', options: {'rs': ['fit', 800, 0]}, pdfPassword: 's3cret', extension: 'png');

// /info, /video, HLS and DASH.
img.infoUrl('s3://photos/cat.jpg', options: {'size': true, 'format': true, 'dimensions': true});
img.videoUrl('s3://media/talk.mov', options: {'rs': ['fill', 640, 360], 'vs': 12, 'vd': 8}, extension: 'webm');
img.video('s3://media/talk.mov').start(12).duration(8).mute(true).extension('mp4').build();
img.streamUrl('s3://media/talk.mov', protocol: StreamProtocol.hls, renditions: [360, 720], segmentDuration: 4);
```

The per-option methods and the `ProcessingOption`, `InfoOption` and `VideoOption` constructors are generated from the server's catalogues, with typed arguments (`int`, `num`, `bool`, `String`) and named records for repeated groups (`ba(2, [(left: 0, top: 0, width: 0.5, height: 0.5)])`). Optional arguments you leave out are not written, and `null` writes an empty argument (`rs('fill', 300, null, true)` is `rs:fill:300::1`). Dart has no overloads, so an option with several argument forms (`gravity`, `crop`, `background`) takes `Object` where the forms disagree. Two names are Dart reserved words, so their methods end in `_`: `do_` (`/info` `detect_objects`) and the `in_` parameter of `fade`.

Invalid input throws `UrlBuildException`, whose `code` is an `ErrorCode` such as `unknownOption`, `invalidArgument` or `invalidSource`, the codes every official client shares.

## Query dialect

```dart
final q = QueryUrlBuilder(baseUrl: 'https://img.example.com', signing: key);
q.url('s3://photos/cat.jpg', params: {'w': 300, 'f': 'webp'}, options: {'bl': 2});
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
q.videoUrl('s3://media/talk.mov', options: {'rs': ['fill', 640, 360], 'vs': 1}, extension: 'webm');
q.streamUrl('s3://media/talk.mov', protocol: StreamProtocol.hls, renditions: [360]);
q.imaginaryUrl('resize', 's3://photos/cat.jpg', params: {'width': 300});
```

## HTTP client

```dart
final xi = XtandardClient('http://localhost:8080', signing: key);
await xi.healthz();
final info = await xi.info('s3://photos/cat.jpg', options: {'size': true, 'format': true}); // typed InfoResponse
print('${info.format} ${info.width}x${info.height}');
final res = await xi.image('s3://photos/cat.jpg', options: {'w': 300}, accept: 'image/avif,image/webp');
final part = await xi.video('s3://media/talk.mov', options: {'vd': 5}, extension: 'webm', range: const ByteRange(0, 1023));
final head = await xi.video('s3://media/talk.mov', options: {'vd': 5}, extension: 'webm', method: HttpMethod.head);
final big = await xi.videoStreamed('s3://media/talk.mov', options: {'vd': 60}, extension: 'mp4'); // listen to big.stream
final manifest = await xi.stream('s3://media/talk.mov', protocol: StreamProtocol.hls, renditions: [360, 720]);
final segment = await xi.media(manifest.body.split('\n').lastWhere((l) => l.isNotEmpty)); // ticket child URLs need no signing
xi.close();
```

`XtandardClient` takes any `http.Client` (`httpClient:`): `IOClient` on servers, `BrowserClient` on the web, [`cronet_http`](https://pub.dev/packages/cronet_http) and [`cupertino_http`](https://pub.dev/packages/cupertino_http) in Flutter, a `RetryClient`, or a `MockClient` in tests. It closes only a client it created. Every call takes `headers:` and an `abortTrigger:` future (`package:http`'s abortable requests), and the constructor takes a per-request `timeout:`.

Non-2xx responses throw an `XtandardHttpException` subclass chosen by status:

- `BadRequestException` (400)
- `ForbiddenException` (403)
- `NotFoundException` (404)
- `NotAcceptableException` (406)
- `UnprocessableException` (422)
- `TooManyRequestsException` (429)
- `ServiceUnavailableException` (503)
- `GatewayException` (502, 504)
- `ServerException` (other 5xx)

Each carries `status`, `kind`, `message`, `requestId` and the parsed `body` from the server's JSON error envelope. Connection failures, timeouts and aborts throw `TransportException`. `XtandardException` is the sealed base of all three kinds, so a `switch` over it is exhaustive.

### HTTP QUERY (opt-in)

Query dialect URLs can go out with the HTTP QUERY method (RFC 10008): the URL's query, `xi_sig` included, becomes the request content (`application/x-www-form-urlencoded`), so long option chains or encrypted sources stay out of URLs. It is off by default, because many CDNs and proxies neither cache nor forward QUERY; the server accepts it unless `XI_SERVER_QUERY_METHOD=false`.

```dart
final req = queryMethodRequest(q.url('s3://photos/cat.jpg', params: {'w': 300})); // method, url, contentType, body
final one = await xi.media(q.url('s3://photos/cat.jpg'), method: HttpMethod.query);
final all = XtandardClient('https://img.example.com', queryMethod: true); // URLs under queryMount, when no method is given
```

A URL without a query (or with a fragment) throws `UrlBuildException` with `ErrorCode.invalidRequest`.

## Flutter

The builders return strings, so they work with any image widget. This helper builds a width-bucketed URL for `Image.network`, so nearby layout sizes share one cached image and an unsigned URL stays within the widths a `restricted` server allows:

```dart
import 'package:flutter/widgets.dart';
import 'package:xtandard_image/xtandard_image.dart';

const img = ImgproxyUrlBuilder(baseUrl: 'https://img.example.com', sourceEncoding: SourceEncoding.plain);
const widths = [160, 320, 640, 960, 1280, 1920]; // the server's allowed widths

String imageUrl(String source, double logicalWidth, double devicePixelRatio) {
  final px = (logicalWidth * devicePixelRatio).ceil();
  final w = widths.firstWhere((w) => w >= px, orElse: () => widths.last);
  return img.image(source).width(w).format('webp').build();
}

class XiImage extends StatelessWidget {
  const XiImage(this.source, {super.key});
  final String source;

  @override
  Widget build(BuildContext context) => LayoutBuilder(
        builder: (context, box) => Image.network(
          imageUrl(source, box.maxWidth, MediaQuery.devicePixelRatioOf(context)),
          fit: BoxFit.cover,
        ),
      );
}
```

For HLS, hand `streamUrl(...)` to a player such as `video_player`.

## Coverage

| Server feature | Dart |
|---|---|
| imgproxy processing options (90 with aliases), typed per option | `ProcessingOption`, fluent methods (`ProcessingMethods`), every name in the shared builder fixture |
| `/info` options (35) | `InfoOption`, `infoUrl()`, `info()`, `XtandardClient.info()` (typed `InfoResponse`) |
| `/video` options (50) and all 12 containers; `/video` signing | `VideoOption`, `videoUrl()`, `video()`, `XtandardClient.video()` with Range and HEAD, `videoStreamed()` |
| HLS and DASH (`st`, `sgd`, `sgf:ts`, `@m3u8`, `@mpd`) | `streamUrl()` on both builders, `XtandardClient.stream()`; ticket child URLs through `media()` |
| Password-protected PDFs (`pdp`) | `pdfPassword:` / `withPdfPassword()`, `encryptPdfPassword()` |
| Sources: base64, `plain/`, `enc/`, split; `xi://`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://` | `sourceEncoding`, `splitSource`, `xiSource()`, `sourceUrl()` |
| Query dialect `/q/`, `/q/video`, imaginary; `xi_sig` | `QueryUrlBuilder`, `signXiUrl()`, `signXiQuery()` |
| HTTP QUERY on the query dialect (opt-in) | `queryMethodRequest()` (`QueryMethodRequest`), `XtandardClient(queryMethod:, queryMount:)`, `media(url, method: HttpMethod.query)` |
| `/healthz`, `/readyz`, playground `api/meta` | `healthz()`, `readyz()`, `playgroundMeta()` |

The shared conformance fixture skips 4 of its 150 cases because the typed API cannot express them: chained `/info` and `/video` pipelines (`infoUrl()` and `videoUrl()` take one options map), a PDF password on `/video` (no such parameter), and an unknown stream protocol (`StreamProtocol` is an enum).

Not covered, on purpose (as in the TypeScript client):

- other dialects' URL shapes, which their own SDKs build;
- imaginary's `sign` scheme;
- presets-only URLs;
- the playground's POST API.

## Development

```sh
cd clients/dart
mise install                 # the Dart SDK pinned in mise.toml
task ci                      # format, analyze, VM and Chrome tests, licences, docs, publish dry run
dart test                    # conformance + builder fixture + vectors + unit + client tests (VM)
dart test -p chrome          # the same in Chrome (dart2js); add -c dart2wasm for WebAssembly
XI_SMOKE_URL=http://127.0.0.1:8080 dart test test/smoke_test.dart   # against the playground demo
```

The browser tests read the repository's fixtures through a hybrid isolate (`spawnHybridCode`), so they run every case too. `lib/src/generated/` is generated from `clients/spec/spec.json` by `task clients:generate` (`clients/codegen/emitters/dart.ts`). Do not edit it. `dart format` runs on everything else.

Runtime dependencies, with their closure (`async`, `collection`, `convert`, `http_parser`, `meta`, `path`, `source_span`, `string_scanner`, `term_glyph`, `typed_data`, `web`, all BSD-3-Clause, from the Dart team):

- `crypto` (BSD-3-Clause): HMAC-SHA256;
- `http` (BSD-3-Clause): the HTTP client;
- `pointycastle` (MIT): AES-256-CBC with PKCS#7. It is pure Dart and synchronous, so URL building stays synchronous on every platform, the web included. The `cryptography` package (Apache-2.0) uses WebCrypto on the web, which is asynchronous only.

`scripts/check_licenses.dart` checks the full closure against MIT, BSD, Apache-2.0 and ISC. There are no exceptions.
