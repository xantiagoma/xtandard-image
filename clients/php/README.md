<p align="center"><img src="https://raw.githubusercontent.com/xantiagoma/xtandard-image/main/docs/assets/logo-128.png" alt="xtandard-image" width="128" height="128"></p>

# xtandard-image for PHP

**Typed URL builder, signing and PSR-18 HTTP client for the xtandard-image server.**

[![Packagist: pending](https://img.shields.io/badge/Packagist-pending-lightgrey)](https://github.com/xantiagoma/xtandard-image/blob/main/docs/RELEASE.md) <!-- registry-status: pending -->
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

`xtandard/image` is the PHP client for [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme), an open-source media server that covers your application's image and video needs, built on libvips and FFmpeg. The package builds and signs the URLs your code sends to the server, and wraps its `/info` and health endpoints on any PSR-18 HTTP client; it does not process images itself. To run a server, see [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md), or start one with signing keys:

```sh
export XI_SIGNING_KEY="$(openssl rand -hex 32)" XI_SIGNING_SALT="$(openssl rand -hex 32)"
docker run -d --name xtandard-image -p 8080:8080 \
  -e XI_SOURCES=web -e XI_SOURCES_WEB_TYPE=http -e XI_SOURCES_WEB_ALLOW=images.unsplash.com \
  -e XI_SIGNING_KEYS="$XI_SIGNING_KEY" -e XI_SIGNING_SALTS="$XI_SIGNING_SALT" \
  ghcr.io/xantiagoma/xtandard-image:0.1.0-rc.2
```

## Install

```sh
composer require xtandard/image
composer require guzzlehttp/guzzle   # or any PSR-18 client, for the HTTP client
```

Not published yet: publishing to Packagist is being set up. Until then, install from the repository: <!-- registry-status: pending -->

```json
{
    "repositories": [{"type": "vcs", "url": "https://github.com/xantiagoma/xtandard-image"}],
    "require": {"xtandard/image": "dev-main"}
}
```

The repository's root `composer.json` describes this package, so Composer installs it straight from the repository.

## Quick start

Build a signed URL with the keys the server was started with:

```php
use Xtandard\Image\ImgproxyUrlBuilder;
use Xtandard\Image\SigningKey;

$signing = new SigningKey(getenv('XI_SIGNING_KEY'), getenv('XI_SIGNING_SALT'));
$img = new ImgproxyUrlBuilder(baseUrl: 'http://localhost:8080', signing: $signing);

$url = $img->image('https://images.unsplash.com/photo-1506744038136-46273834b3fb')
    ->resize('fill', 1200, 630)
    ->gravity('sm')
    ->extension('jpg')
    ->build();
// http://localhost:8080/<signature>/rs:fill:1200:630/g:sm/aHR0cHM6….jpg
```

Ask the server about the source with the HTTP client (here on Guzzle):

```php
use GuzzleHttp\Client as Guzzle;
use GuzzleHttp\Psr7\HttpFactory;
use Xtandard\Image\Client;

$xi = new Client('http://localhost:8080', new Guzzle(['timeout' => 30]), new HttpFactory(), signing: $signing);

$info = $xi->info('https://images.unsplash.com/photo-1506744038136-46273834b3fb');
echo $info['format'], ' ', $info['width'], ' ', $info['height']; // jpeg 7360 4912
```

Sign URLs on a server only: the signing key signs any URL.

## Usage

### Building URLs

There are two equivalent forms, an options array and a fluent chain with one method per option name and alias (camelCase: `minWidth` for `min-width`):

```php
$img = new ImgproxyUrlBuilder(baseUrl: 'https://img.example.com');

$img->url('s3://photos/cat.jpg', ['resize' => ['fill', 300, 400], 'gravity' => 'sm', 'quality' => 80], 'webp');
$img->image('s3://photos/cat.jpg')->resize('fill', 300, 400)->g('sm')->quality(80)->extension('webp')->build();
// both: https://img.example.com/insecure/rs:fill:300:400/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp
```

Keys are option names or aliases; values are argument lists or a bare first argument. The option catalogues are generated as PHPStan/Psalm array shapes (`ProcessingOptions`, `InfoOptions`, `VideoOptions` in `Xtandard\Image\OptionTypes`), so a wrong enum value or argument type fails static analysis. The options themselves are documented in [URL dialects §3.2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#32-processing-options--tr).

Builder settings: `separator` (default `:`), `unsignedSignature` (`insecure`, `unsafe` or `_`), `optionNames` (`short` by default, `canonical` or `given`) and `splitSource`.

### Chained pipelines

`Pipelines`, or `pipeline()` in the fluent form, writes `/-/` between pipelines:

```php
use Xtandard\Image\Pipelines;

$img->url('s3://photos/cat.jpg', new Pipelines(['rs' => ['fit', 500, 500]], ['bl' => 2]));
$img->image('s3://photos/cat.jpg')->rs('fit', 500, 500)->pipeline()->bl(2)->build();
```

### Sources

| `sourceEncoding` | URL |
|---|---|
| `'base64'` (default) | `/…/czM6Ly9waG90b3MvY2F0LmpwZw.webp` |
| `'plain'` | `/…/plain/https://example.com/a%20b.jpg`, escaped for you |
| `'encrypted'` | `/…/enc/<token>`; needs `encryption` (below) |

Set it on the builder or per call (`$img->url($source, ['w' => 300], sourceEncoding: 'plain')`). `xiSource('photos', '2026/cat.jpg')` writes a named source (`xi://photos/2026/cat.jpg`) and `sourceUrl('s3', 'bucket', 'cat.jpg')` a storage URL; both are functions in the `Xtandard\Image` namespace.

### `/info`

```php
$img->infoUrl('s3://photos/cat.jpg', ['size' => true, 'format' => true]);
$img->info('s3://photos/cat.jpg')->size(true)->format(true)->build();
```

### Video and streams

`/video` URLs use the video option catalogue; the extension is the container (`mp4`, `webm`, `mp3`, `png` for a waveform, …). `streamUrl()` builds HLS and DASH manifests, whose segments a player fetches without any key:

```php
use Xtandard\Image\StreamProtocol;

$img->video('s3://media/talk.mov')->rs('fill', 640, 360)->vs(12)->vd(5)->extension('webm')->build();
$img->streamUrl('s3://media/talk.mov', StreamProtocol::Hls, renditions: [360, 720], segmentDuration: 4);
```

The server needs the `full` or `gpl` image for video. Options and containers: [URL dialects §3.8](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#38-video-transformations-wp-122-wp-123) and [§3.10](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#310-hls-and-dash-streams-wp-126-every-dialect).

### Query dialect

`queryUrls()` builds `/q/` URLs (native keys plus imgproxy options in `o=`), `/q/video` and imaginary endpoints, signed with `xi_sig`. The server needs `XI_DIALECTS_QUERY_ENABLED=true`.

```php
use function Xtandard\Image\queryUrls;

$q = queryUrls(baseUrl: 'https://img.example.com', signing: $signing);
$q->url('s3://photos/cat.jpg', ['w' => 300, 'fit' => 'cover'], ['bl' => 2], 'webp');
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&fit=cover&o=bl:2&format=webp&xi_sig=…
$q->imaginaryUrl('smartcrop', 's3://photos/cat.jpg', ['width' => 300, 'height' => 300]);
```

See [URL dialects §4](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#4-query-dialect-native--imaginary).

### Signing and encryption

`SigningKey` takes the server's hex key and salt (`XI_SIGNING_KEYS` / `XI_SIGNING_SALTS`, or imgproxy's `IMGPROXY_KEY` / `IMGPROXY_SALT`). `EncryptionKey` takes the first `XI_ENCRYPTION_KEYS` entry, for `enc/` sources and password-protected PDFs:

```php
use Xtandard\Image\EncryptionKey;

$img = new ImgproxyUrlBuilder(baseUrl: $baseUrl, signing: $signing, encryption: new EncryptionKey(getenv('XI_ENCRYPTION_KEY')));
$img->url('s3://private/cat.jpg', ['w' => 300], sourceEncoding: 'encrypted');
$img->url('s3://docs/contract.pdf', ['rs' => ['fit', 800, 0]], pdfPassword: 's3cret');
```

Each `enc/` token gets a random IV. `SigningKey::raw()` and `EncryptionKey::raw()` take raw bytes, and neither key shows its secrets in `var_dump()`. The functions `sign()`, `signImgproxyPath()`, `signXiUrl()`, `signXiQuery()`, `encryptSource()` and `encryptPdfPassword()` are exported too; `signXiUrl()` adds an `xi_sig` to a URL of any other dialect. Schemes: [URL dialects §2](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md#2-signing-schemes-at-a-glance).

### HTTP client

`Client` works with any [PSR-18](https://www.php-fig.org/psr/psr-18/) client and [PSR-17](https://www.php-fig.org/psr/psr-17/) request factory (Symfony's `Psr18Client` is both). It has `healthz()`, `readyz()`, `info()`, `image()`, `video()`, `stream()`, `media()` (with `Range` and `HEAD`) and `playgroundMeta()`, and returns PSR-7 responses:

```php
use Xtandard\Image\ByteRange;

$res = $xi->image('s3://photos/cat.jpg', ['w' => 300], accept: 'image/avif,image/webp');
$part = $xi->video('s3://media/talk.mov', ['vd' => 5], 'webm', range: new ByteRange(0, 65535));
```

In Laravel, bind `ImgproxyUrlBuilder` and `Client` as singletons in a service provider, with the keys from `config/services.php`.

### Errors

The builders throw `UrlBuildException`, whose `$errorCode` (`ErrorCode::UnknownOption`, `InvalidArgument`, `InvalidSource`, …) names the fault. The client throws an `HttpException` subclass by status (`ForbiddenException`, `NotFoundException`, `TooManyRequestsException`, …) with the server's `$status`, `$kind` and `$requestId`, and `TransportException` when the connection fails. All of them implement `Xtandard\Image\Exception\XtandardException`.

```php
use Xtandard\Image\Exception\HttpException;

try {
    $xi->info('https://example.com/a.jpg');
} catch (HttpException $e) {
    echo $e->status, ' ', $e->kind, ' ', $e->requestId; // 403 forbidden …
}
```

## Compatibility

- PHP 8.2 to 8.5, with `ext-openssl`. The only runtime dependencies are the PSR interfaces `psr/http-client`, `psr/http-factory` and `psr/http-message`.
- The package version is the server version: `xtandard/image` 0.2.1 matches `ghcr.io/xantiagoma/xtandard-image:0.2.1`. Versions follow [ZeroVer](https://0ver.org): a minor bump may break the API, a patch does not.

## Links

- [xtandard-image](https://github.com/xantiagoma/xtandard-image#readme) and [Getting started](https://github.com/xantiagoma/xtandard-image/blob/main/docs/GETTING-STARTED.md)
- [URL dialects](https://github.com/xantiagoma/xtandard-image/blob/main/docs/URL-DIALECTS.md): every option and signing scheme
- [Playground](https://github.com/xantiagoma/xtandard-image/blob/main/docs/PLAYGROUND.md): build URLs in a browser and copy the PHP code
- [Changelog](https://github.com/xantiagoma/xtandard-image/blob/main/CHANGELOG.md)
- [Contributing](https://github.com/xantiagoma/xtandard-image/blob/main/CONTRIBUTING.md) and [the official clients](https://github.com/xantiagoma/xtandard-image/blob/main/clients/README.md)
- [Security policy](https://github.com/xantiagoma/xtandard-image/blob/main/SECURITY.md)
- Licence: [MIT](https://github.com/xantiagoma/xtandard-image/blob/main/LICENSE)

A personal project, provided as is under the MIT licence: see the [disclaimer](https://github.com/xantiagoma/xtandard-image#disclaimer).

Design: every official client is generated from one spec and passes one conformance fixture ([ADR 0013](https://github.com/xantiagoma/xtandard-image/blob/main/docs/adr/0013-official-clients.md)).
