# xtandard-image for PHP

The official PHP client of [xtandard-image](https://github.com/xantiagoma/xtandard-image) ([ADR 0013](../../docs/adr/0013-official-clients.md)). It has:

- typed URL builders for the imgproxy dialect (processing, `/info`, `/video`, HLS and DASH) and the query dialect (`/q/`, `/q/video`, imaginary endpoints);
- imgproxy and `xi_sig` signing (`hash_hmac`);
- `enc/` source and PDF password encryption (`openssl_encrypt`, AES-CBC);
- an HTTP client on any [PSR-18](https://www.php-fig.org/psr/psr-18/) client and [PSR-17](https://www.php-fig.org/psr/psr-17/) request factory, with no hard dependency on Guzzle or Symfony.

It needs PHP 8.2 or later with `ext-openssl`. Its only runtime dependencies are the PSR interface packages `psr/http-client`, `psr/http-factory` and `psr/http-message` (MIT). Every file has `declare(strict_types=1)`. The option catalogues are generated as PHPStan/Psalm array shapes and the code is clean under PHPStan at level max with the strict rules.

It is not on Packagist yet. The package `xtandard/image` (the name is free; checked 2026-10-15) will follow the server's release tags.

**Build and sign URLs on a server.** The signing key signs every URL, and the encryption key decrypts every `enc/` source and PDF password.

## Install

Packagist reads this repository's **root `composer.json`**, which describes this package and autoloads `clients/php/src` and `clients/php/generated` (ADR 0013); there is no split or mirror repository. After the first release:

```sh
composer require xtandard/image
```

Release zips leave out the shared test fixtures (`/testdata export-ignore` in `.gitattributes`), so an install downloads about 6 MB instead of 20 MB. Before a release, use a path repository on a checkout (the repository root, or `clients/php` for development):

```json
{
    "repositories": [{"type": "path", "url": "../xtandard-image"}],
    "require": {"xtandard/image": "@dev"},
    "minimum-stability": "dev"
}
```

`clients/php/composer.json` is the development manifest (tests, PHPStan, php-cs-fixer); keep its `require` and `autoload` in sync with the root one.

For the HTTP client, also install a PSR-18 client, for example `guzzlehttp/guzzle` or `symfony/http-client` with `nyholm/psr7`.

## URL builder

```php
use Xtandard\Image\EncryptionKey;
use Xtandard\Image\ImgproxyUrlBuilder;
use Xtandard\Image\Pipelines;
use Xtandard\Image\SigningKey;
use Xtandard\Image\StreamProtocol;
use function Xtandard\Image\xiSource;

$img = new ImgproxyUrlBuilder(
    baseUrl: 'https://img.example.com',
    signing: new SigningKey(getenv('XI_SIGNING_KEY'), getenv('XI_SIGNING_SALT')),   // hex, as in the server config
    encryption: new EncryptionKey(getenv('XI_ENCRYPTION_KEY')),                    // only for enc/ sources and PDF passwords
);

// Options array: keys are option names or aliases, values are argument lists or a bare first argument.
$img->url('s3://photos/cat.jpg', ['rs' => ['fill', 1200, 630], 'g' => 'sm', 'q' => 80], 'webp');
// https://img.example.com/<signature>/rs:fill:1200:630/g:sm/q:80/czM6Ly9waG90b3MvY2F0LmpwZw.webp

// Fluent form: one method per option name and alias (camelCase: minWidth for min-width).
$img->image(xiSource('photos', '2026/cat.jpg'))->resize('fit', 300, 300)->blur(2)->extension('png')->build();

// Chained pipelines (/-/): Pipelines, or pipeline() in the fluent form.
$img->url('s3://photos/cat.jpg', new Pipelines(['rs' => ['fit', 500, 500]], ['bl' => 2]));

// Plain or encrypted sources, a password-protected PDF.
$img->url('https://example.com/a b.jpg', ['w' => 300], sourceEncoding: 'plain');
$img->url('s3://private/cat.jpg', ['w' => 300], sourceEncoding: 'encrypted');
$img->url('s3://docs/report.pdf', ['rs' => ['fit', 800, 0]], 'png', pdfPassword: 's3cret');

// /info, /video, HLS and DASH.
$img->infoUrl('s3://photos/cat.jpg', ['size' => true, 'format' => true, 'dimensions' => true]);
$img->videoUrl('s3://media/talk.mov', ['rs' => ['fill', 640, 360], 'vs' => 12, 'vd' => 8], 'webm');
$img->video('s3://media/talk.mov')->start(12)->duration(8)->mute(true)->extension('mp4')->build();
$img->streamUrl('s3://media/talk.mov', StreamProtocol::Hls, renditions: [360, 720], segmentDuration: 4);
```

`SigningKey::raw()` and `EncryptionKey::raw()` take raw bytes instead of hex. Neither key shows its secrets in `var_dump()` or `print_r()`, and every secret parameter is `#[\SensitiveParameter]`. The function forms `imgproxy()`, `queryUrls()`, `sign()`, `signXiUrl()`, `encryptSource()`, `xiSource()`, `rangeHeader()` and the rest live in the `Xtandard\Image` namespace.

### Types

`generated/OptionTypes.php` declares one PHPStan/Psalm type per option and the options shapes `ProcessingOptions`, `InfoOptions` and `VideoOptions`; `generated/ResponseTypes.php` declares `InfoResponse` and `ErrorResponse`. They come from the server's catalogues. Import them where you build options:

```php
/** @phpstan-import-type ProcessingOptions from \Xtandard\Image\OptionTypes */
```

A wrong enum value (`['rs' => ['stretch', 300]]`), a wrong argument type (`['q' => 'high']`), an unknown fluent method or an unknown `InfoResponse` key fails static analysis; `tests-static/usage.php` is the proof that `task types` checks. PHPStan's array shapes are unsealed, so an unknown option *name* in an options array is reported by Psalm but not by PHPStan. At run time, every mistake throws `UrlBuildException`, whose `$errorCode` (`ErrorCode::UnknownOption`, `InvalidArgument`, `InvalidSource`, …) is the language-neutral code of the conformance fixture. Enum parameters take the generated enums (`StreamProtocol`, `SegmentFormat`, `SourceEncoding`, `ImageFormat`, `VideoContainer`, `SourceScheme`) or their string values.

## Query dialect

```php
use function Xtandard\Image\queryUrls;

$q = queryUrls(baseUrl: 'https://img.example.com', signing: new SigningKey($key, $salt));
$q->url('s3://photos/cat.jpg', ['w' => 300, 'f' => 'webp'], ['bl' => 2]);
// https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2&xi_sig=…
$q->videoUrl('s3://media/talk.mov', ['rs' => ['fill', 640, 360], 'vs' => 1], 'webm');
$q->streamUrl('s3://media/talk.mov', 'hls', renditions: [360]);
$q->imaginaryUrl('resize', 's3://photos/cat.jpg', ['width' => 300]);
$q->image('s3://photos/cat.jpg')->param('w', 300)->blur(2)->build();
```

## HTTP client

```php
use GuzzleHttp\Client as Guzzle;
use GuzzleHttp\Psr7\HttpFactory;
use Xtandard\Image\ByteRange;
use Xtandard\Image\Client;
use Xtandard\Image\Exception\ForbiddenException;

$xi = new Client('http://localhost:8080', new Guzzle(['timeout' => 30]), new HttpFactory(), signing: new SigningKey($key, $salt));
$xi->healthz();
$info = $xi->info('s3://photos/cat.jpg', ['size' => true, 'format' => true]);   // InfoResponse array shape
$res = $xi->image('s3://photos/cat.jpg', ['w' => 300], accept: 'image/avif,image/webp');
$part = $xi->video('s3://media/talk.mov', ['vd' => 5], 'webm', range: new ByteRange(0, 1023));
$head = $xi->video('s3://media/talk.mov', ['vd' => 5], 'webm', method: 'HEAD');
$manifest = $xi->stream('s3://media/talk.mov', 'hls', renditions: [360, 720]);
$segment = $xi->media('/_stream/<ticket>/v0.m3u8');   // ticket child URLs need no signing
```

With Symfony HttpClient, `Psr18Client` is both the client and the request factory:

```php
use Symfony\Component\HttpClient\Psr18Client;

$xi = new Client('http://localhost:8080', new Psr18Client(), signing: new SigningKey($key, $salt));
```

Responses are PSR-7 `ResponseInterface`s; read large bodies through `getBody()` as a stream. Non-2xx responses throw an `HttpException` subclass chosen by status:

- `BadRequestException` (400)
- `ForbiddenException` (403)
- `NotFoundException` (404)
- `NotAcceptableException` (406)
- `UnprocessableException` (422)
- `TooManyRequestsException` (429)
- `ServiceUnavailableException` (503)
- `GatewayException` (502, 504)
- `ServerException` (other 5xx)

Each carries `$status`, `$kind`, the message, `$requestId` from the server's JSON error envelope, and the `$response`. Connection failures throw `TransportException`, with the PSR-18 exception as the previous one. Every exception implements `Xtandard\Image\Exception\XtandardException`.

### HTTP QUERY (opt-in)

The query dialect also accepts the HTTP QUERY method (RFC 10008): the URL's query moves into the request content, so long option chains, watermark text or encrypted sources stay out of URLs. It is off by default, because many CDNs and proxies neither cache nor forward QUERY; the server needs `XI_SERVER_QUERY_METHOD` (on by default).

```php
use Xtandard\Image\QueryMethodRequest;

$q = QueryMethodRequest::fromUrl($queryUrls->url('s3://photos/cat.jpg', ['w' => 300]));   // or queryMethodRequest($url)
// $q->method 'QUERY', $q->url '/q/', $q->contentType 'application/x-www-form-urlencoded', $q->body 'url=…&w=300&xi_sig=…'

$res = $xi->media($signedQueryUrl, 'QUERY');                    // one call
$xi = new Client($origin, $http, $factory, queryMethod: true);  // every media() URL under queryMount ('/q') with no method given
```

The content is the built URL's query as written, `xi_sig` included (the signature does not cover the method). A URL without a query, or with a fragment, throws `UrlBuildException` with `ErrorCode::InvalidRequest`. HEAD and explicit GET are unchanged. The content stream comes from a PSR-17 `StreamFactoryInterface`: the `streamFactory:` argument, or the request factory or PSR-18 client when it is one (Guzzle's `HttpFactory` and Symfony's `Psr18Client` are).

## Frameworks

### Laravel

A service provider that binds the URL builder and the client from `config/services.php` (Laravel ships Guzzle):

```php
namespace App\Providers;

use GuzzleHttp\Client as Guzzle;
use GuzzleHttp\Psr7\HttpFactory;
use Illuminate\Support\ServiceProvider;
use Xtandard\Image\Client;
use Xtandard\Image\ImgproxyUrlBuilder;
use Xtandard\Image\SigningKey;

final class XtandardImageServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->app->singleton(SigningKey::class, fn () => new SigningKey(
            config('services.xtandard.key'),     // env('XI_SIGNING_KEY')
            config('services.xtandard.salt'),    // env('XI_SIGNING_SALT')
        ));
        $this->app->singleton(ImgproxyUrlBuilder::class, fn ($app) => new ImgproxyUrlBuilder(
            baseUrl: config('services.xtandard.url'),
            signing: $app->make(SigningKey::class),
        ));
        $this->app->singleton(Client::class, fn ($app) => new Client(
            config('services.xtandard.url'),
            new Guzzle(['timeout' => 30]),
            new HttpFactory(),
            signing: $app->make(SigningKey::class),
        ));
    }
}
```

```blade
<img src="{{ app(\Xtandard\Image\ImgproxyUrlBuilder::class)->url($photo->source, ['rs' => ['fill', 640, 480]], 'webp') }}" alt="">
```

### WordPress

A filter that serves attachment images through xtandard-image, resized to the requested size (the keys belong in `wp-config.php`, never in a theme):

```php
use Xtandard\Image\ImgproxyUrlBuilder;
use Xtandard\Image\SigningKey;

add_filter('wp_get_attachment_image_src', static function ($image, $attachmentId, $size) {
    static $img = null;
    $img ??= new ImgproxyUrlBuilder(
        baseUrl: XI_URL,
        signing: new SigningKey(XI_SIGNING_KEY, XI_SIGNING_SALT),
    );
    if (!is_array($image) || !$image[1] || !$image[2]) {
        return $image;
    }
    $source = wp_get_attachment_url($attachmentId);   // or an s3:// or xi:// source
    $image[0] = $img->url($source, ['rs' => ['fit', $image[1], $image[2]]], 'webp');

    return $image;
}, 10, 3);
```

## Coverage

| Server feature | PHP |
|---|---|
| imgproxy processing options (90 with aliases), typed per option | `ProcessingOptions` shape, fluent methods, every name in the shared builder fixture |
| `/info` options (35) | `InfoOptions`, `infoUrl()`, `info()`, `Client::info()` (typed `InfoResponse`) |
| `/video` options (50) and all 12 containers; `/video` signing | `VideoOptions`, `videoUrl()`, `video()`, `Client::video()` with Range and HEAD |
| HLS and DASH (`st`, `sgd`, `sgf:ts`, `@m3u8`, `@mpd`) | `streamUrl()` on both builders, `Client::stream()`; ticket child URLs through `Client::media()` |
| Password-protected PDFs (`pdp`) | `pdfPassword:` / `withPdfPassword()`, `Encryption::encryptPdfPassword()` |
| Sources: base64, `plain/`, `enc/`, split; `xi://`, `s3://`, `gs://`, `abs://`, `local:///`, `ftp://`, `sftp://` | `sourceEncoding`, `splitSource`, `Sources::xiSource()`, `Sources::sourceUrl()` |
| Query dialect `/q/`, `/q/video`, imaginary; `xi_sig` | `QueryUrlBuilder` (native keys typed as `QueryImageParams`), `Signing::signXiUrl()` |
| HTTP QUERY on the query dialect (opt-in) | `QueryMethodRequest::fromUrl()` / `queryMethodRequest()`, `Client::media($url, 'QUERY')`, `Client(queryMethod: true, queryMount: …)` |
| `/healthz`, `/readyz`, playground `api/meta` | `healthz()`, `readyz()`, `playgroundMeta()` |

Not covered, on purpose (as in the TypeScript client):

- other dialects' URL shapes, which their own SDKs build;
- imaginary's `sign` scheme;
- presets-only URLs;
- the playground's POST API.

The conformance fixture skips one case: `videoUrl()` has no `pdfPassword` parameter (as in Python), so `err_video_pdf_password` cannot be expressed.

## Development

```sh
cd clients/php
mise install        # PHP pinned in mise.toml (built from source; Composer comes with it)
task ci             # = task -d clients/php ci from the root: install, lint, types, tests, licences, build
task test           # PHPUnit: conformance + builder fixture + vectors + unit + client tests
XI_SMOKE_URL=http://127.0.0.1:8080 task test -- tests/SmokeTest.php   # against the playground demo
```

- `generated/` is written from `clients/spec/spec.json` by `task clients:generate` (`clients/codegen/emitters/php.ts`). Do not edit it. Composer maps `Xtandard\Image\` to both `src/` and `generated/`.
- `task lint` runs php-cs-fixer with PER Coding Style 2.0 (the successor of PSR-12).
- `task types` runs PHPStan at level max with the strict rules over `src`, `generated`, `tests` and `scripts`, then `scripts/check-static-types.php`. The fixture-driven tests pass untyped JSON into the typed API on purpose; `argument.type` is ignored in those two files only.
- The client tests run `tests/Support/server.php` under PHP's built-in web server and talk to it through Guzzle, a real PSR-18 client (a dev dependency).
- `composer.lock` pins the development tools for PHP 8.2 (`config.platform.php`), so one lock file serves the CI matrix (8.2 to 8.5).
- `scripts/check-licenses.php` checks the runtime closure (the three PSR packages, MIT) against MIT, BSD, Apache-2.0 and ISC. There are no exceptions.
- The toolchain comes from mise. On macOS, mise compiles PHP with the Homebrew build dependencies (`icu4c`, `libxml2`, `openssl`, `pkg-config` and others); no Docker image is needed.
