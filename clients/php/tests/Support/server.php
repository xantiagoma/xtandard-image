<?php

declare(strict_types=1);

// A stand-in xtandard-image for the client tests, run with PHP's built-in
// server (php -S 127.0.0.1:<port> tests/Support/server.php) by StandInServer.
// It answers like the real server: health, readiness, /info JSON, /video with
// Range and HEAD (and a signature check), an HLS manifest, the playground's
// api/meta, and JSON or non-JSON errors.

require dirname(__DIR__, 2) . '/vendor/autoload.php';

use Xtandard\Image\Tests\Support\StandInServer;

$server = static fn(string $key): string => is_string($_SERVER[$key] ?? null) ? $_SERVER[$key] : '';
$method = $server('REQUEST_METHOD');
$uri = $server('REQUEST_URI');
// Not parse_url(): it fails on imgproxy paths such as /info/<sig>/s:1/<source>.
$path = explode('?', $uri, 2)[0];

/** @param array<mixed> $body */
$json = static function (int $status, array $body): void {
    http_response_code($status);
    header('Content-Type: application/json');
    echo json_encode($body, \JSON_THROW_ON_ERROR);
};

// Echo what the client sent, for header tests.
header('X-Seen-Accept: ' . $server('HTTP_ACCEPT'));
header('X-Seen-Custom: ' . $server('HTTP_X_CUSTOM'));
header('X-Seen-Method: ' . $method);

if ('/healthz' === $path) {
    header('Content-Type: text/plain');
    echo "ok\n";

    return;
}
if ('/readyz' === $path) {
    http_response_code(503);
    header('Content-Type: text/plain');
    echo "draining\n";

    return;
}
if (str_starts_with($path, '/info/')) {
    $json(200, ['width' => 640, 'height' => 480, 'format' => 'jpeg']);

    return;
}
if (str_starts_with($path, '/video/') && str_ends_with($path, '.m3u8')) {
    header('Content-Type: application/vnd.apple.mpegurl');
    echo "#EXTM3U\n/_stream/T/v0.m3u8\n";

    return;
}
if (str_starts_with($path, '/video/') || str_starts_with($path, '/_stream/')) {
    if (str_starts_with($path, '/video/') && $uri !== StandInServer::videoPath()) {
        $json(403, ['error' => ['kind' => 'unauthorized', 'message' => 'invalid signature']]);

        return;
    }
    $body = StandInServer::body();
    header('Content-Type: video/webm');
    header('Accept-Ranges: bytes');
    if (1 === preg_match('/^bytes=(\d+)-(\d*)$/', $server('HTTP_RANGE'), $m)) {
        $start = (int) $m[1];
        $end = '' === $m[2] ? strlen($body) - 1 : (int) $m[2];
        http_response_code(206);
        header("Content-Range: bytes {$start}-{$end}/" . strlen($body));
        if ('HEAD' !== $method) {
            echo substr($body, $start, $end - $start + 1);
        }

        return;
    }
    header('Content-Length: ' . strlen($body));
    if ('HEAD' !== $method) {
        echo $body;
    }

    return;
}
if ('/_admin/play/api/meta' === $path) {
    $json(200, ['version' => 'test']);

    return;
}
if ('/proxy-error' === $path) {
    http_response_code(502);
    header('Content-Type: text/html');
    echo '<html>bad gateway</html>';

    return;
}
if ('/busy' === $path) {
    $json(429, ['error' => ['kind' => 'overloaded', 'message' => 'too many requests', 'request_id' => 'r1']]);

    return;
}
if ('/future' === $path) {
    $json(500, ['error' => ['kind' => 'brand_new_kind', 'message' => 'something new']]);

    return;
}
if ('/list' === $path) {
    $json(200, ['a', 'b']);

    return;
}
$json(404, ['error' => ['kind' => 'not_found', 'message' => 'source image not found']]);
