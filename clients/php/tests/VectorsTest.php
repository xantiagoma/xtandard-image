<?php

declare(strict_types=1);

namespace Xtandard\Image\Tests;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Xtandard\Image\Encryption;
use Xtandard\Image\EncryptionKey;
use Xtandard\Image\ErrorCode;
use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\Signing;
use Xtandard\Image\SigningKey;

/**
 * testdata/security/vectors.json: the signing, enc/ and pdf_password vectors shared with Go and TypeScript.
 */
final class VectorsTest extends TestCase
{
    private const FILE = 'testdata/security/vectors.json';

    /** @return array<string, array{array<string, mixed>}> */
    public static function imgproxy(): array
    {
        return Fixture::rows(self::FILE, 'imgproxy');
    }

    /** @return array<string, array{array<string, mixed>}> */
    public static function xiSig(): array
    {
        return Fixture::rows(self::FILE, 'xi_sig');
    }

    /** @return array<string, array{array<string, mixed>}> */
    public static function encryption(): array
    {
        return Fixture::rows(self::FILE, 'encryption');
    }

    /** @return array<string, array{array<string, mixed>}> */
    public static function pdfPassword(): array
    {
        return Fixture::rows(self::FILE, 'pdf_password');
    }

    /** @param array<string, mixed> $v */
    #[DataProvider('imgproxy')]
    public function testImgproxy(array $v): void
    {
        $k = new SigningKey(Fixture::str($v, 'key'), Fixture::str($v, 'salt'), Fixture::optInt($v, 'size') ?? 32);
        $path = Fixture::str($v, 'path');
        $sig = Fixture::str($v, 'signature');
        self::assertSame($sig, Signing::sign($path, $k));
        $p = str_starts_with($path, '/') ? $path : '/' . $path;
        self::assertSame("/{$sig}{$p}", Signing::signImgproxyPath($path, $k));
        $raw = SigningKey::raw((string) hex2bin(Fixture::str($v, 'key')), (string) hex2bin(Fixture::str($v, 'salt')), Fixture::optInt($v, 'size') ?? 32);
        self::assertSame($sig, Signing::sign($path, $raw));
    }

    /** @param array<string, mixed> $v */
    #[DataProvider('xiSig')]
    public function testXiSig(array $v): void
    {
        $k = new SigningKey(Fixture::str($v, 'key'), Fixture::str($v, 'salt'), Fixture::optInt($v, 'size') ?? 32);
        $path = Fixture::str($v, 'path');
        $signed = Fixture::str($v, 'signed_query');
        self::assertSame($signed, Signing::signXiQuery($path, Fixture::str($v, 'query'), $k));
        self::assertSame($signed, Signing::signXiQuery($path, $signed, $k));
        self::assertSame(
            "https://img.example.com{$path}?{$signed}",
            Signing::signXiUrl("https://img.example.com{$path}?" . Fixture::str($v, 'query'), $k),
        );
    }

    /** @param array<string, mixed> $v */
    #[DataProvider('encryption')]
    public function testEncryption(array $v): void
    {
        $token = Encryption::encryptSource(Fixture::str($v, 'plaintext'), new EncryptionKey(Fixture::str($v, 'key'), Fixture::str($v, 'iv')));
        self::assertSame(Fixture::str($v, 'token'), $token);
        self::assertSame(Fixture::str($v, 'plaintext'), self::decrypt($token, Fixture::str($v, 'key')));
    }

    /** @param array<string, mixed> $v */
    #[DataProvider('pdfPassword')]
    public function testPdfPassword(array $v): void
    {
        self::assertSame(
            Fixture::str($v, 'token'),
            Encryption::encryptPdfPassword(Fixture::str($v, 'password'), new EncryptionKey(Fixture::str($v, 'key'), Fixture::str($v, 'iv'))),
        );
    }

    public function testRandomIv(): void
    {
        $first = array_values(self::pdfPassword())[0] ?? self::fail('no pdf_password vectors');
        $hex = Fixture::str($first[0], 'key');
        $k = new EncryptionKey($hex);
        $a = Encryption::encryptPdfPassword('s3cret', $k);
        $b = Encryption::encryptPdfPassword('s3cret', $k);
        self::assertNotSame($a, $b);
        self::assertSame('s3cret', self::decrypt($a, $hex));
        self::assertSame('s3cret', self::decrypt(Encryption::encryptSource('s3cret', EncryptionKey::raw((string) hex2bin($hex))), $hex));
    }

    public function testXiMessage(): void
    {
        $m = Signing::xiMessage('/', 'url=x&&xi_sig=s&w=1');
        self::assertSame('s', $m->signature);
        self::assertSame('/?url=x&w=1', $m->message);
        $none = Signing::xiMessage('/', '');
        self::assertNull($none->signature);
        self::assertSame('/', $none->message);
        try {
            Signing::xiMessage('/', 'xi_sig=a&xi_sig=b');
            self::fail('two xi_sig accepted');
        } catch (UrlBuildException $e) {
            self::assertSame(ErrorCode::InvalidRequest, $e->errorCode);
        }
    }

    public function testKeysHideSecrets(): void
    {
        $k = new SigningKey(str_repeat('ab', 32), str_repeat('cd', 32));
        $e = new EncryptionKey(str_repeat('ef', 32), str_repeat('01', 16));
        $dump = print_r($k, true) . print_r($e, true);
        ob_start();
        var_dump($k, $e);
        $dump .= (string) ob_get_clean();
        foreach (["\xab\xab", "\xcd\xcd", "\xef\xef", 'abab', 'cdcd', 'efef'] as $secret) {
            self::assertStringNotContainsString($secret, $dump);
        }
    }

    private static function decrypt(string $token, string $hexKey): string
    {
        $raw = (string) base64_decode(strtr($token, '-_', '+/'), true);
        $key = (string) hex2bin($hexKey);
        $out = openssl_decrypt(substr($raw, 16), 'aes-' . (\strlen($key) * 8) . '-cbc', $key, \OPENSSL_RAW_DATA, substr($raw, 0, 16));
        self::assertIsString($out);

        return $out;
    }
}
