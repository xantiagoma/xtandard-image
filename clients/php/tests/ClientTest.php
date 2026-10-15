<?php

declare(strict_types=1);

namespace Xtandard\Image\Tests;

use GuzzleHttp\Client as Guzzle;
use GuzzleHttp\Psr7\HttpFactory;
use PHPUnit\Framework\TestCase;
use Psr\Http\Client\ClientInterface;
use Psr\Http\Message\RequestFactoryInterface;
use Psr\Http\Message\RequestInterface;
use Psr\Http\Message\ResponseInterface;
use Xtandard\Image\ByteRange;
use Xtandard\Image\ByteSuffix;
use Xtandard\Image\Client;
use Xtandard\Image\ErrorCode;
use Xtandard\Image\Exception\BadRequestException;
use Xtandard\Image\Exception\ForbiddenException;
use Xtandard\Image\Exception\GatewayException;
use Xtandard\Image\Exception\HttpException;
use Xtandard\Image\Exception\NotFoundException;
use Xtandard\Image\Exception\ServerException;
use Xtandard\Image\Exception\ServiceUnavailableException;
use Xtandard\Image\Exception\TooManyRequestsException;
use Xtandard\Image\Exception\TransportException;
use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\StreamProtocol;
use Xtandard\Image\Tests\Support\StandInServer;

/**
 * The HTTP client against a stand-in server (PHP's built-in web server running
 * tests/Support/server.php) through a real PSR-18 client (Guzzle).
 */
final class ClientTest extends TestCase
{
    private static ?StandInServer $server = null;

    public static function setUpBeforeClass(): void
    {
        self::$server = StandInServer::start();
    }

    public static function tearDownAfterClass(): void
    {
        self::$server?->stop();
        self::$server = null;
    }

    private static function base(): string
    {
        return (self::$server ?? self::fail('no stand-in server'))->baseUrl;
    }

    private static function client(string $playgroundPath = '/_admin/play/'): Client
    {
        $factory = new HttpFactory();

        return new Client(
            self::base(),
            new Guzzle(['timeout' => 10]),
            $factory,
            signing: StandInServer::signing(),
            headers: ['X-Custom' => 'yes'],
            playgroundPath: $playgroundPath,
        );
    }

    public function testHealthAndReadiness(): void
    {
        $xi = self::client();
        $xi->healthz();
        $r = $xi->readyz();
        self::assertFalse($r->ready);
        self::assertSame('draining', $r->state);
    }

    public function testInfo(): void
    {
        $info = self::client()->info('s3://b/cat.jpg', ['size' => true, 'format' => true]);
        self::assertSame(640, $info['width'] ?? null);
        self::assertSame('jpeg', $info['format'] ?? null);
        self::assertSame(480, self::client()->infoAt('/info/x/y')['height'] ?? null);
    }

    public function testVideoRangeAndHead(): void
    {
        $xi = self::client();
        $full = $xi->video('s3://m/clip.mov', ['vd' => 2], 'webm');
        self::assertSame(200, $full->getStatusCode());
        self::assertSame(StandInServer::body(), (string) $full->getBody());
        self::assertSame('*/*', $full->getHeaderLine('X-Seen-Accept'));
        self::assertSame('yes', $full->getHeaderLine('X-Seen-Custom'));

        $part = $xi->video('s3://m/clip.mov', ['vd' => 2], 'webm', range: new ByteRange(10, 19));
        self::assertSame(206, $part->getStatusCode());
        self::assertSame(substr(StandInServer::body(), 10, 10), (string) $part->getBody());
        self::assertSame('bytes 10-19/1000', $part->getHeaderLine('Content-Range'));

        $open = $xi->video('s3://m/clip.mov', ['vd' => 2], 'webm', range: new ByteRange(990));
        self::assertSame(substr(StandInServer::body(), 990), (string) $open->getBody());

        $head = $xi->video('s3://m/clip.mov', ['vd' => 2], 'webm', method: 'HEAD');
        self::assertSame(200, $head->getStatusCode());
        self::assertSame('HEAD', $head->getHeaderLine('X-Seen-Method'));
        self::assertSame('', (string) $head->getBody());
        self::assertStringStartsWith('video/webm', $head->getHeaderLine('Content-Type'));
    }

    public function testWrongSignatureIsForbidden(): void
    {
        try {
            self::client()->video('s3://m/other.mov', ['vd' => 2], 'webm');
            self::fail('no exception');
        } catch (ForbiddenException $e) {
            self::assertSame(403, $e->status);
            self::assertSame(403, $e->getCode());
            self::assertSame('unauthorized', $e->kind);
            self::assertSame('invalid signature', $e->getMessage());
            self::assertStringContainsString('/video/', $e->url);
            self::assertNull($e->requestId);
        }
    }

    public function testStream(): void
    {
        $xi = self::client();
        $m = $xi->stream('s3://m/clip.mov', StreamProtocol::Hls, renditions: [360]);
        self::assertStringStartsWith('#EXTM3U', $m->body);
        self::assertSame('application/vnd.apple.mpegurl', $m->contentType);
        self::assertStringEndsWith('.m3u8', $m->url);
        $child = '/_stream/T/v0.m3u8';
        self::assertSame(200, $xi->media($child)->getStatusCode());
        self::assertSame(200, $xi->media(self::base() . $child)->getStatusCode());
    }

    public function testPlaygroundMeta(): void
    {
        self::assertSame(['version' => 'test'], self::client()->playgroundMeta());
    }

    public function testErrors(): void
    {
        $xi = self::client();
        $cases = [
            ['/missing', NotFoundException::class, 'not_found', 'source image not found', null],
            ['/busy', TooManyRequestsException::class, 'overloaded', 'too many requests', 'r1'],
            ['/proxy-error', GatewayException::class, 'unknown', '<html>bad gateway</html>', null],
            ['/future', ServerException::class, 'unknown', 'something new', null],
        ];
        foreach ($cases as [$path, $class, $kind, $message, $rid]) {
            try {
                $xi->media($path);
                self::fail("{$path}: no exception");
            } catch (HttpException $e) {
                self::assertInstanceOf($class, $e, $path);
                self::assertSame($kind, $e->kind, $path);
                self::assertSame($message, $e->getMessage(), $path);
                self::assertSame($rid, $e->requestId, $path);
                self::assertNotNull($e->response);
            }
        }
        $this->expectException(HttpException::class);
        $this->expectExceptionMessage('not a JSON object');
        $xi->infoAt('/list');
    }

    public function testExceptionClasses(): void
    {
        self::assertSame(BadRequestException::class, HttpException::classFor(400));
        self::assertSame(ServiceUnavailableException::class, HttpException::classFor(503));
        self::assertSame(GatewayException::class, HttpException::classFor(504));
        self::assertSame(ServerException::class, HttpException::classFor(500));
        self::assertSame(HttpException::class, HttpException::classFor(418));
        self::assertInstanceOf(ServerException::class, HttpException::fromStatus(503, '/', 'x'));
    }

    public function testTransportError(): void
    {
        $xi = new Client('http://127.0.0.1:' . StandInServer::freePort(), new Guzzle(['timeout' => 2]), new HttpFactory());
        $this->expectException(TransportException::class);
        $xi->healthz();
    }

    public function testRangeHeader(): void
    {
        self::assertSame('bytes=0-1023', Client::rangeHeader(new ByteRange(0, 1023)));
        self::assertSame('bytes=-500', Client::rangeHeader(new ByteSuffix(500)));
        self::assertSame('bytes=5-', Client::rangeHeader(new ByteRange(5)));
        try {
            Client::rangeHeader(new ByteSuffix(-1));
            self::fail('negative suffix accepted');
        } catch (UrlBuildException $e) {
            self::assertSame(ErrorCode::InvalidRange, $e->errorCode);
        }
    }

    public function testRequestFactoryFromClient(): void
    {
        $both = new class implements ClientInterface, RequestFactoryInterface {
            public function sendRequest(RequestInterface $request): ResponseInterface
            {
                $f = new HttpFactory();

                return $f->createResponse(200)->withBody($f->createStream("ready\n"));
            }

            public function createRequest(string $method, $uri): RequestInterface
            {
                return (new HttpFactory())->createRequest($method, $uri);
            }
        };
        self::assertTrue((new Client('http://xi.test', $both))->readyz()->ready);

        $only = new class implements ClientInterface {
            public function sendRequest(RequestInterface $request): ResponseInterface
            {
                throw new \LogicException('unused');
            }
        };
        $this->expectException(\InvalidArgumentException::class);
        new Client('http://xi.test', $only);
    }
}
