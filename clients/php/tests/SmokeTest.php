<?php

declare(strict_types=1);

namespace Xtandard\Image\Tests;

use GuzzleHttp\Client as Guzzle;
use GuzzleHttp\Psr7\HttpFactory;
use PHPUnit\Framework\TestCase;
use Xtandard\Image\ByteRange;
use Xtandard\Image\Client;
use Xtandard\Image\Exception\NotFoundException;
use Xtandard\Image\QueryUrlBuilder;
use Xtandard\Image\SourceEncoding;
use Xtandard\Image\Sources;
use Xtandard\Image\StreamProtocol;

/**
 * Smoke test against a running server, skipped unless XI_SMOKE_URL is set.
 *
 * The playground demo (compose.playground.yml, unsigned URLs allowed) works:
 *
 *     XI_SMOKE_URL=http://127.0.0.1:8080 vendor/bin/phpunit tests/SmokeTest.php
 *
 * It needs the demo's local fixtures (xi://local/…) and, for the /video
 * cases, a full or gpl image with XI_VIDEO_TRANSFORM_ENABLED.
 */
final class SmokeTest extends TestCase
{
    private static function base(): string
    {
        $base = getenv('XI_SMOKE_URL');
        if (!\is_string($base) || '' === $base) {
            self::markTestSkipped('set XI_SMOKE_URL to run against a server');
        }

        return $base;
    }

    private static function client(SourceEncoding $encoding = SourceEncoding::Base64): Client
    {
        return new Client(self::base(), new Guzzle(['timeout' => 60]), new HttpFactory(), sourceEncoding: $encoding);
    }

    private static function image(): string
    {
        return Sources::xiSource('local', 'jpeg/baseline.jpg');
    }

    private static function video(): string
    {
        return Sources::xiSource('local', 'video/bbb-h264-aac.mp4');
    }

    public function testHealthAndImage(): void
    {
        $xi = self::client(SourceEncoding::Plain);
        $xi->healthz();
        self::assertTrue($xi->readyz()->ready);
        $res = $xi->image(self::image(), ['rs' => ['fit', 64, 64]], 'webp');
        self::assertSame('image/webp', $res->getHeaderLine('Content-Type'));
        self::assertSame('RIFF', substr((string) $res->getBody(), 0, 4));
        $info = $xi->info(self::image(), ['size' => true, 'format' => true, 'dimensions' => true]);
        self::assertSame('jpeg', $info['format'] ?? null);
        self::assertGreaterThan(0, $info['width'] ?? 0);
        $this->expectException(NotFoundException::class);
        $xi->image(Sources::xiSource('local', 'jpeg/missing.jpg'));
    }

    public function testQueryDialect(): void
    {
        $url = (new QueryUrlBuilder(baseUrl: self::base()))->url(self::image(), ['w' => 32, 'f' => 'png']);
        self::assertSame('image/png', self::client()->media($url)->getHeaderLine('Content-Type'));
    }

    public function testVideoRangeAndStream(): void
    {
        $xi = self::client();
        $res = $xi->video(self::video(), ['vd' => 1, 'w' => 160], 'webm', range: new ByteRange(0, 99));
        self::assertContains($res->getStatusCode(), [200, 206]);
        $head = $xi->video(self::video(), ['vd' => 1, 'w' => 160], 'webm', method: 'HEAD');
        self::assertStringStartsWith('video/webm', $head->getHeaderLine('Content-Type'));
        $m = $xi->stream(self::video(), StreamProtocol::Hls, renditions: [240]);
        self::assertStringStartsWith('#EXTM3U', $m->body);
        $child = null;
        foreach (explode("\n", $m->body) as $line) {
            if ('' !== $line && !str_starts_with($line, '#')) {
                $child = $line;
                break;
            }
        }
        self::assertNotNull($child);
        self::assertSame(200, $xi->media($child)->getStatusCode());
    }
}
