![xtandard-image](https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png)

# xtandard-image for Dart

**Typed URL builders, signing and an HTTP client for the xtandard-image server, for Flutter apps and Dart servers.**

[//]: # (registry-status: pending)
[![pub.dev: pending](https://img.shields.io/badge/pub.dev-pending-lightgrey)](https://github.com/xantiagoma/xtandard-image/blob/main/docs/RELEASE.md) [![license: MIT](https://img.shields.io/badge/license-MIT-blue)](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

`xtandard_image` is the Dart client for [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme), an open-source media server that covers your application's image and video needs, built on libvips and FFmpeg. The package builds and signs the URLs your code sends to the server, and wraps its `/info`, media and health endpoints; it does not process images itself. It is pure Dart: it runs in Flutter (iOS, Android, web, desktop) and on servers (shelf, dart_frog, serverpod). To run a server, see [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md), or start one with signing keys:

```sh
export XI_SIGNING_KEY="$(openssl rand -hex 32)" XI_SIGNING_SALT="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web -e XI_SOURCES_WEB_TYPE=http -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS="$XI_SIGNING_KEY" -e XI_SIGNING_SALTS="$XI_SIGNING_SALT" \
  ghcr.io/xantiagoma/xtandard-image:0.1.0-rc.2
```

## Install

```sh
dart pub add xtandard_image   # or: flutter pub add xtandard_image
```

[//]: # (registry-status: pending)
Not published yet: publishing to pub.dev is being set up. Until then, install from the repository:

```yaml
dependencies:
  xtandard_image:
    git:
      url: https://github.com/xantiagoma/xtandard-image
      path: clients/dart
```

## Quick start

Build a signed URL with the keys the server was started with (on a server; see [Keys in apps](#keys-in-apps)):

```dart
import 'dart:io';

import 'package:xtandard_image/xtandard_image.dart';

final signing = SigningKey(
  key: Platform.environment['XI_SIGNING_KEY']!,
  salt: Platform.environment['XI_SIGNING_SALT']!,
);
final img = ImgproxyUrlBuilder(baseUrl: 'http://localhost:8080', signing: signing);

final url = img
    .image('https://images.unsplash.com/photo-1506744038136-46273834b3fb')
    .resize('fill', 1200, 630)
    .gravity('sm')
    .extension('jpg')
    .build();
// http://localhost:8080/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
```

Ask the server about the source with the HTTP client:

```dart
final xi = XtandardClient('http://localhost:8080', signing: signing);
final info = await xi.info('https://images.unsplash.com/photo-1506744038136-46273834b3fb');
print('${info.format} ${info.width}x${info.height}'); // jpeg 7360x4912
xi.close();
```

## Keys in apps

The signing key signs every URL, and the encryption key decrypts every `enc/` source and PDF password. On a server, read them from the environment. In an app they work the same way but stay optional, because **a key embedded in an app can be extracted** from the binary or the JavaScript bundle. Prefer one of these:

- **Unsigned URLs under the server's `restricted` policy**: the server accepts unsigned URLs only for presets and allowed sizes (its startup log lists the widths), and the app builds plain unsigned URLs.
- **Backend-signed URLs**: your backend signs URLs with this package or any official client, and the app only displays them.

## Usage

### Building URLs

There are two equivalent forms, an options map (names or aliases to argument lists, or a bare first argument) and a fluent chain with one typed method per option name and alias:

```dart
final img = ImgproxyUrlBuilder(baseUrl: 'https://img.example.com', signing: signing);

img.url('s3://photos/cat.jpg', options: {'rs': ['fill', 1200, 630], 'g': 'sm', 'q': 80}, extension: 'webp');
// https://img.example.com/<signature>/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp
img.image(xiSource('photos', '2026/cat.jpg')).resize('fit', 300, 300).blur(2).extension('png').build();
img.image('s3://photos/cat.jpg').addAll([ProcessingOption.resize('fill', 600, 400), ProcessingOption.quality(75)]).build();
```

The methods and the `ProcessingOption`, `InfoOption` and `VideoOption` values are generated from the server's catalogues. `null` writes an empty argument (`rs('fill', 300, null, true)` is `rs:fill:300::1`). Two names are Dart reserved words, so they end in `_` (`do_`, and the `in_` parameter of `fade`). The options themselves are documented in [URL dialects §3.2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#32-processing-options--tr).

### Chained pipelines

`pipelines:`, or `pipeline()` in the fluent form, writes `/-/` between pipelines:

```dart
img.url('s3://photos/cat.jpg', options: {'rs': ['fit', 500, 500]}, pipelines: [{'bl': 2}]);
img.image('s3://photos/cat.jpg').resize('fit', 500, 500).pipeline().blur(2).build();
```

### Sources

Sources are base64url by default (`splitSource:` splits them into segments). `SourceEncoding.plain` writes `/plain/…`, escaped for you, and `SourceEncoding.encrypted` writes `/enc/<token>` (it needs `encryption:`, below). Set it on the builder or per call:

```dart
img.url('https://example.com/a b.jpg', options: {'w': 300}, sourceEncoding: SourceEncoding.plain);
// …/plain/https://example.com/a%20b.jpg
```

`xiSource('photos', '2026/cat.jpg')` writes a named source (`xi://photos/2026/cat.jpg`) and `sourceUrl(SourceScheme.s3, 'bucket', 'cat.jpg')` a storage URL.

### `/info`

```dart
img.infoUrl('s3://photos/cat.jpg', options: {'size': true, 'format': true, 'dimensions': true});
img.info('s3://photos/cat.jpg').size(true).format(true).build();
```

### Video and streams

`/video` URLs use the video option catalogue; the extension is the container (`mp4`, `webm`, `mp3`, `png` for a waveform, …). `streamUrl` builds HLS and DASH manifests, whose segments a player (such as `video_player`) fetches without any key:

```dart
img.video('s3://media/talk.mov').start(12).duration(8).mute(true).extension('mp4').build();
img.streamUrl('s3://media/talk.mov', protocol: StreamProtocol.hls, renditions: [360, 720], segmentDuration: 4);
```

The server needs the `full` or `gpl` image for video. Options and containers: [URL dialects §3.8](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123) and [§3.10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#310-hls-and-dash-streams-wp-126-every-dialect).

### Query dialect

`QueryUrlBuilder` builds `/q/` URLs (native keys plus imgproxy options in `o=`), `/q/video` and imaginary endpoints, signed with `xi_sig`. The server needs `XI_DIALECTS_QUERY_ENABLED=true`.

```dart
final q = QueryUrlBuilder(baseUrl: 'https://img.example.com', signing: signing);
q.url('s3://photos/cat.jpg', params: {'w': 300, 'f': 'webp'}, options: {'bl': 2});
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
q.imaginaryUrl('resize', 's3://photos/cat.jpg', params: {'width': 300});
```

See [URL dialects §4](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#4-query-dialect-native--imaginary). The client can also send query URLs with the HTTP QUERY method (`queryMethodRequest()`, `XtandardClient(queryMethod: true)`), off by default.

### Signing and encryption

`SigningKey` takes the server's hex key and salt (`XI_SIGNING_KEYS` / `XI_SIGNING_SALTS`, or imgproxy's `IMGPROXY_KEY` / `IMGPROXY_SALT`). `EncryptionKey` takes the first `XI_ENCRYPTION_KEYS` entry, for `enc/` sources and password-protected PDFs:

```dart
final img = ImgproxyUrlBuilder(
  baseUrl: 'https://img.example.com',
  signing: signing,
  encryption: EncryptionKey(key: Platform.environment['XI_ENCRYPTION_KEY']!),
);
img.url('s3://private/cat.jpg', options: {'w': 300}, sourceEncoding: SourceEncoding.encrypted);
img.url('s3://docs/report.pdf', options: {'rs': ['fit', 800, 0]}, pdfPassword: 's3cret', extension: 'png');
```

`signXiUrl()` adds an `xi_sig` to a URL of any other dialect. Schemes: [URL dialects §2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#2-signing-schemes-at-a-glance).

### HTTP client

`XtandardClient` has `healthz`, `readyz`, `info`, `image`, `video`, `videoStreamed`, `stream`, `media` (with Range and HEAD) and `playgroundMeta`. It takes any `http.Client` (`httpClient:`): `IOClient` on servers, `BrowserClient` on the web, `cronet_http` or `cupertino_http` in Flutter, or a `MockClient` in tests. Every call takes `headers:` and an `abortTrigger:`:

```dart
final res = await xi.image('s3://photos/cat.jpg', options: {'w': 300}, accept: 'image/avif,image/webp');
final part = await xi.video('s3://media/talk.mov', options: {'vd': 5}, extension: 'webm', range: const ByteRange(0, 1023));
```

In Flutter the builders return strings, so they work with `Image.network` or any image widget. Round widths up to the sizes a `restricted` server allows, so nearby layouts share one cached image.

### Errors

Invalid input throws `UrlBuildException`, whose `code` is an `ErrorCode` (`unknownOption`, `invalidArgument`, `invalidSource`, …), the codes every official client shares. A non-2xx answer throws an `XtandardHttpException` subclass chosen by status (`ForbiddenException`, `NotFoundException`, `TooManyRequestsException`, `ServiceUnavailableException`, …), with the server's `status`, `kind` and `requestId`. Connection failures, timeouts and aborts throw `TransportException`. `XtandardException` is the sealed base of all of them, so a `switch` over it is exhaustive.

```dart
try {
  await xi.info('https://example.com/a.jpg');
} on XtandardHttpException catch (e) {
  print('${e.status} ${e.kind} ${e.requestId}'); // 403 forbidden …
}
```

## Compatibility

- Dart 3.5 or later (Flutter 3.24 or later), on the VM, AOT, dart2js and dart2wasm. It does not depend on Flutter.
- Runtime dependencies: `crypto` and `http` (BSD-3-Clause, Dart team) and `pointycastle` (MIT, pure Dart AES-CBC, so URL building stays synchronous on the web too).
- The package version is the server version: `xtandard_image` 0.2.1 matches `ghcr.io/xantiagoma/xtandard-image:0.2.1`. Versions follow [ZeroVer](https://0ver.org): a minor bump may break the API, a patch does not.

## Links

- [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme) and [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md)
- [URL dialects](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md): every option and signing scheme
- [Playground](https://github.com/xantiagoma/xtandard-image/blob/main/docs/PLAYGROUND.md): build URLs in a browser and copy the Dart code
- [Changelog](https://github.com/xantiagoma/xtandard-image/blob/main/CHANGELOG.md)
- [Contributing](https://github.com/xantiagoma/xtandard-image/blob/main/CONTRIBUTING.md) and [the official clients](https://github.com/xantiagoma/xtandard-image/blob/main/clients/README.md)
- [Security policy](https://github.com/xantiagoma/xtandard-image/blob/main/SECURITY.md)
- Licence: [MIT](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

A personal project, provided as is under the MIT licence: see the [disclaimer](https://github.com/xantiagoma/xtandard-image#disclaimer).

Design: every official client is generated from one spec and passes one conformance fixture ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)).
