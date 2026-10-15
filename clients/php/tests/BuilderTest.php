<?php

declare(strict_types=1);

namespace Xtandard\Image\Tests;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Xtandard\Image\EncryptionKey;
use Xtandard\Image\ErrorCode;
use Xtandard\Image\ErrorKind;
use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\ImageFormat;
use Xtandard\Image\ImgproxyUrlBuilder;
use Xtandard\Image\Internal\Text;
use Xtandard\Image\OptionNameStyle;
use Xtandard\Image\Pipelines;
use Xtandard\Image\QueryUrlBuilder;
use Xtandard\Image\SegmentFormat;
use Xtandard\Image\SigningKey;
use Xtandard\Image\SourceEncoding;
use Xtandard\Image\SourceScheme;
use Xtandard\Image\StreamProtocol;
use Xtandard\Image\VideoContainer;

use function Xtandard\Image\escapePlainSource;
use function Xtandard\Image\imgproxy;
use function Xtandard\Image\queryUrls;
use function Xtandard\Image\rangeHeader;
use function Xtandard\Image\sign;
use function Xtandard\Image\sourceUrl;
use function Xtandard\Image\xiSource;

/**
 * Unit tests of the builders beyond the shared fixtures: the PHP forms (enums,
 * typed per-option methods, function helpers) and the number format.
 */
final class BuilderTest extends TestCase
{
    private const SRC = 'https://example.com/images/cat.jpg';

    /** @return array<string, array{int|float, string}> */
    public static function numbers(): array
    {
        return [
            'int' => [300, '300'],
            'whole float' => [300.0, '300'],
            'tenth' => [0.1, '0.1'],
            'one and a half' => [1.5, '1.5'],
            'negative' => [-2.25, '-2.25'],
            'negative zero' => [-0.0, '0'],
            'small' => [1e-7, '1e-7'],
            'small fraction' => [0.000001, '0.000001'],
            'small mantissa' => [1.5e-7, '1.5e-7'],
            'large' => [1e21, '1e+21'],
            'just under 1e21' => [1e20, '100000000000000000000'],
            'large mantissa' => [1.2345e22, '1.2345e+22'],
            'third' => [1 / 3, '0.3333333333333333'],
            'float sum' => [0.1 + 0.2, '0.30000000000000004'],
            'max int' => [\PHP_INT_MAX, '9223372036854775807'],
        ];
    }

    #[DataProvider('numbers')]
    public function testFormatNumber(int|float $v, string $want): void
    {
        self::assertSame($want, Text::formatNumber($v, 'test'));
    }

    public function testNonFiniteNumber(): void
    {
        $this->expectExceptionObject(new UrlBuildException(ErrorCode::InvalidArgument, 'option dpr argument 1: INF is not a finite number'));
        (new ImgproxyUrlBuilder())->url(self::SRC, ['dpr' => \INF]);
    }

    public function testOptionsForms(): void
    {
        $img = new ImgproxyUrlBuilder();
        $want = '/insecure/rs:fill:300:400/q:80/g:sm/aHR0cHM6Ly9leGFtcGxlLmNvbS9pbWFnZXMvY2F0LmpwZw.webp';
        self::assertSame($want, $img->url(self::SRC, ['resize' => ['fill', 300, 400], 'quality' => 80, 'g' => 'sm', 'bl' => null], ImageFormat::Webp));
        self::assertSame($want, $img->image(self::SRC)->resize('fill', 300, 400)->quality(80)->gravity('sm')->extension(ImageFormat::Webp)->build());
        self::assertSame($want, $img->image(self::SRC)->options(['rs' => ['fill', 300, 400], 'q' => 80, 'g' => 'sm'])->extension('webp')->build());
        self::assertSame(
            '/insecure/rs:fit:500:500/-/bl:2/aHR0cHM6Ly9leGFtcGxlLmNvbS9pbWFnZXMvY2F0LmpwZw',
            $img->url(self::SRC, new Pipelines(['rs' => ['fit', 500, 500]], ['bl' => 2])),
        );
        self::assertSame('/insecure/rs:fill:300::1/aHR0cHM6Ly9leGFtcGxlLmNvbS9pbWFnZXMvY2F0LmpwZw', $img->url(self::SRC, ['rs' => ['fill', 300, null, true]]));
        // A hyphenated name gets a camelCase method (min-width → minWidth).
        self::assertSame('/insecure/mw:10/aHR0cHM6Ly9leGFtcGxlLmNvbS9pbWFnZXMvY2F0LmpwZw', $img->image(self::SRC)->minWidth(10)->build());
        self::assertSame(
            '/insecure/resize:fit:1:1/aHR0cHM6Ly9leGFtcGxlLmNvbS9pbWFnZXMvY2F0LmpwZw',
            (string) (new ImgproxyUrlBuilder(optionNames: OptionNameStyle::Canonical))->image(self::SRC)->rs('fit', 1, 1),
        );
    }

    public function testEnumsAndStrings(): void
    {
        $img = new ImgproxyUrlBuilder(signing: new SigningKey('00', '00'), sourceEncoding: SourceEncoding::Plain);
        $a = $img->streamUrl('s3://m/a.mov', StreamProtocol::Hls, [360], 4, SegmentFormat::Ts);
        $b = $img->streamUrl('s3://m/a.mov', 'hls', [360], 4, 'ts');
        self::assertSame($a, $b);
        self::assertStringEndsWith('/st:hls:360/sgd:4/sgf:ts/plain/s3://m/a.mov@m3u8', $a);
        self::assertSame(
            $img->videoUrl('s3://m/a.mov', ['vd' => 1], VideoContainer::Webm),
            $img->videoUrl('s3://m/a.mov', ['vd' => 1], 'webm'),
        );
        self::assertSame('m3u8', StreamProtocol::Hls->container());
        self::assertSame('application/dash+xml', StreamProtocol::Dash->contentType());
        self::assertSame(403, ErrorKind::Unauthorized->status());
        self::assertSame('s3://b/k.jpg', sourceUrl(SourceScheme::S3, 'b', '/k.jpg'));
        self::assertSame('xi://photos/a.jpg', xiSource('photos', 'a.jpg'));
    }

    public function testUnknownEnumStrings(): void
    {
        foreach ([
            // @phpstan-ignore argument.type (PHPStan rejects it; the runtime check is what is tested)
            static fn(): string => (new ImgproxyUrlBuilder())->url(self::SRC, sourceEncoding: 'rot13'),
            // @phpstan-ignore argument.type
            static fn(): string => (new ImgproxyUrlBuilder())->streamUrl(self::SRC, 'smooth'),
            static fn(): string => (new ImgproxyUrlBuilder())->streamUrl(self::SRC, 'dash', segmentFormat: 'ts'),
        ] as $i => $fn) {
            try {
                $fn();
                self::fail("case {$i}: no exception");
            } catch (UrlBuildException $e) {
                self::assertSame(ErrorCode::InvalidRequest, $e->errorCode, "case {$i}");
            }
        }
        $this->expectException(UrlBuildException::class);
        sourceUrl('nfs', 'host', 'a'); // @phpstan-ignore argument.type
    }

    public function testUnsupportedArgumentType(): void
    {
        $this->expectExceptionObject(new UrlBuildException(ErrorCode::InvalidArgument, 'option q: unsupported argument type stdClass'));
        // @phpstan-ignore argument.type (the runtime check is what is tested)
        (new ImgproxyUrlBuilder())->url(self::SRC, ['q' => [new \stdClass()]]);
    }

    public function testUnicodeArgumentAndSource(): void
    {
        $img = new ImgproxyUrlBuilder(sourceEncoding: 'plain');
        self::assertSame('/insecure/fn:caf%C3%A9/plain/https://e.com/caf%C3%A9%20%E2%9C%93.jpg@png', $img->url('https://e.com/café ✓.jpg', ['fn' => 'caf%C3%A9'], 'png'));
        self::assertSame('a%20b%25c', escapePlainSource('a b%c'));
        $this->expectException(UrlBuildException::class);
        $img->url(self::SRC, ['fn' => "a\u{2003}b"]); // EM SPACE is whitespace
    }

    public function testQueryForms(): void
    {
        $q = new QueryUrlBuilder(baseUrl: 'https://img.example.com/');
        $want = 'https://img.example.com/q/?url=s3://b/cat.jpg&w=300&f=webp&enlarge=true&o=bl:2';
        self::assertSame($want, $q->url('s3://b/cat.jpg', ['w' => 300, 'f' => 'webp', 'enlarge' => true, 'q' => null], ['bl' => 2]));
        self::assertSame($want, $q->url('s3://b/cat.jpg', [['w', 300], ['f', 'webp'], ['enlarge', true], ['q', null]], ['bl' => 2]));
        self::assertSame($want, $q->image('s3://b/cat.jpg')->param('w', 300)->param('f', 'webp')->param('enlarge', true)->blur(2)->build());
        self::assertSame('https://img.example.com/q/video?url=s3://m/a.mov&c=10,10&format=webm', $q->video('s3://m/a.mov')->crop(10, 10)->extension('webm')->build());
        self::assertSame('https://img.example.com/q/resize?url=s3://b/c.jpg&width=300', $q->imaginaryUrl('resize', 's3://b/c.jpg', ['width' => 300]));
        self::assertSame('/q/?url=x', queryUrls()->url('x'));
    }

    public function testPdfPassword(): void
    {
        $img = imgproxy(signing: new SigningKey('00', '00'), encryption: new EncryptionKey(str_repeat('00', 32), str_repeat('00', 16)));
        $url = $img->image('s3://d/r.pdf')->withPdfPassword('s3cret')->rs('fit', 800, 0)->extension('png')->build();
        self::assertMatchesRegularExpression('#^/[\w-]+/pdp:[\w-]+/rs:fit:800:0/czM6Ly9kL3IucGRm\.png$#', $url);
        self::assertStringContainsString('/pdf_password:', (new ImgproxyUrlBuilder(signing: new SigningKey('00', '00'), encryption: new EncryptionKey(str_repeat('00', 16)), optionNames: 'canonical'))->url('s3://d/r.pdf', pdfPassword: 'x'));
    }

    public function testFunctions(): void
    {
        self::assertSame('bytes=1-2', rangeHeader('bytes=1-2'));
        self::assertSame(sign('/a', new SigningKey('00', '00')), sign('a', new SigningKey('00', '00')));
    }

    public function testKeyValidation(): void
    {
        foreach ([
            static fn(): SigningKey => new SigningKey('zz', '00'),
            static fn(): SigningKey => new SigningKey('0', '00'),
            static fn(): SigningKey => new SigningKey('00', '00', 33),
            static fn(): EncryptionKey => new EncryptionKey('00'),
            static fn(): EncryptionKey => new EncryptionKey(str_repeat('00', 16), '00'),
        ] as $i => $fn) {
            try {
                $fn();
                self::fail("case {$i}: no exception");
            } catch (UrlBuildException $e) {
                self::assertSame(ErrorCode::InvalidKey, $e->errorCode, "case {$i}");
            }
        }
        // Hex in either case, surrounding whitespace ignored (as in the server config).
        self::assertSame(['fixed_iv' => false], (new EncryptionKey(" 00112233445566778899AABBCCDDEEFF\n"))->__debugInfo());
    }
}
