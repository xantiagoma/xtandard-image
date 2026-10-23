<?php

declare(strict_types=1);

namespace Xtandard\Image\Tests;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Xtandard\Image\ByteRange;
use Xtandard\Image\ByteSuffix;
use Xtandard\Image\Encryption;
use Xtandard\Image\EncryptionKey;
use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\ImgproxyUrlBuilder;
use Xtandard\Image\Pipelines;
use Xtandard\Image\QueryMethodRequest;
use Xtandard\Image\QueryUrlBuilder;
use Xtandard\Image\Signing;
use Xtandard\Image\SigningKey;
use Xtandard\Image\Sources;
use Xtandard\Image\UrlChain;

/**
 * clients/spec/conformance.json: the cases every official client runs (ADR 0013).
 *
 * Positive cases must produce expect.value byte for byte; error cases must
 * throw UrlBuildException with expect.error as its code. The fixture's options
 * are pipelines of [name, args] pairs; they are built twice, as options arrays
 * and through the fluent chains (option()), and both must agree.
 */
final class ConformanceTest extends TestCase
{
    private const FILE = 'clients/spec/conformance.json';

    /** @return array<string, array{array<string, mixed>}> */
    public static function cases(): array
    {
        return Fixture::rows(self::FILE, 'cases', 'op');
    }

    public function testFixtureCoversEveryOp(): void
    {
        $ops = array_unique(array_map(static fn(array $row): string => Fixture::str($row[0], 'op'), self::cases()));
        self::assertGreaterThanOrEqual(17, \count($ops));
        self::assertGreaterThanOrEqual(100, \count(self::cases()));
    }

    /** @param array<string, mixed> $case */
    #[DataProvider('cases')]
    public function testCase(array $case): void
    {
        $expect = Fixture::arr($case, 'expect');
        $error = Fixture::optStr($expect, 'error');
        if (null !== $error) {
            $this->assertCode($error, fn(): string => self::runMapping($case));
            try {
                $fluent = self::fluent($case);
            } catch (UrlBuildException $e) {
                self::assertSame($error, $e->errorCode->value, 'fluent form');

                return;
            }
            if (null !== $fluent) {
                $this->assertCode($error, static fn(): string => $fluent->build());
            }

            return;
        }
        $want = Fixture::str($expect, 'value');
        self::assertSame($want, self::runMapping($case));
        $fluent = self::fluent($case);
        if (null !== $fluent) {
            self::assertSame($want, $fluent->build(), 'fluent form');
            self::assertSame($want, (string) $fluent, 'string cast');
        }
    }

    /** @param \Closure(): string $fn */
    private function assertCode(string $code, \Closure $fn): void
    {
        try {
            $got = $fn();
        } catch (UrlBuildException $e) {
            self::assertSame($code, $e->errorCode->value, $e->getMessage());

            return;
        }
        self::fail("want error {$code}, got {$got}");
    }

    /** @param array<string, mixed> $case */
    private static function runMapping(array $case): string
    {
        $cfg = Fixture::arr($case, 'config');
        $in = Fixture::arr($case, 'input');
        $op = Fixture::str($case, 'op');
        $source = Fixture::optStr($in, 'source') ?? '';
        $enc = Fixture::optStr($in, 'source_encoding');
        $ext = Fixture::optStr($in, 'extension');
        $pdp = Fixture::optStr($in, 'pdf_password');

        return match ($op) {
            'imgproxy.url' => self::imgproxy($cfg)->url($source, self::chain($in), $ext, $enc, $pdp),
            'imgproxy.info_url' => self::imgproxy($cfg)->infoUrl($source, self::one($in), $enc, $pdp),
            'imgproxy.video_url' => self::videoUrl($cfg, $in),
            'imgproxy.stream_url' => self::imgproxy($cfg)->streamUrl(
                $source,
                Fixture::str($in, 'protocol'),
                self::renditions($in),
                Fixture::optInt($in, 'segment_duration'),
                Fixture::optStr($in, 'segment_format'),
                self::one($in),
                $enc,
            ),
            'query.url' => self::query($cfg)->url($source, self::params($in), self::chain($in), $ext, $pdp),
            'query.video_url' => self::query($cfg)->videoUrl($source, self::chain($in), $ext),
            'query.stream_url' => self::query($cfg)->streamUrl(
                $source,
                Fixture::str($in, 'protocol'),
                self::renditions($in),
                Fixture::optInt($in, 'segment_duration'),
                Fixture::optStr($in, 'segment_format'),
                self::one($in),
            ),
            'query.imaginary_url' => self::query($cfg)->imaginaryUrl(Fixture::str($in, 'endpoint'), $source, self::params($in)),
            // input.build names the query op that builds the URL from the other inputs.
            'query.query_request' => self::queryRequest(QueryMethodRequest::fromUrl(self::runMapping(['op' => Fixture::str($in, 'build')] + $case))),
            'sign' => Signing::sign(Fixture::str($in, 'message'), self::required(self::signing($cfg))),
            'sign_xi_url' => Signing::signXiUrl(Fixture::str($in, 'url'), self::required(self::signing($cfg)), Fixture::optStr($in, 'mount') ?? ''),
            'encrypt_source' => Encryption::encryptSource($source, self::required(self::encryption($cfg))),
            'encrypt_pdf_password' => Encryption::encryptPdfPassword(Fixture::str($in, 'password'), self::required(self::encryption($cfg))),
            'xi_source' => Sources::xiSource(Fixture::str($in, 'name'), Fixture::str($in, 'key')),
            'source_url' => Sources::sourceUrl(Fixture::str($in, 'scheme'), Fixture::str($in, 'authority'), Fixture::str($in, 'key')),
            'range_header' => ByteRange::header(self::range($in['range'] ?? null)),
            'escape_plain_source' => Sources::escapePlainSource(Fixture::str($in, 'value')),
            'escape_query_value' => Sources::escapeQueryValue(Fixture::str($in, 'value')),
            default => throw new \RuntimeException("unknown op {$op}"),
        };
    }

    private static function queryRequest(QueryMethodRequest $q): string
    {
        return "{$q->method} {$q->url}\nContent-Type: {$q->contentType}\n\n{$q->body}";
    }

    /**
     * @param array<mixed> $cfg
     * @param array<mixed> $in
     */
    private static function videoUrl(array $cfg, array $in): string
    {
        if (\array_key_exists('pdf_password', $in)) {
            self::markTestSkipped('not expressible: videoUrl() has no pdfPassword parameter');
        }

        return self::imgproxy($cfg)->videoUrl(
            Fixture::str($in, 'source'),
            self::one($in),
            Fixture::optStr($in, 'extension'),
            Fixture::optStr($in, 'source_encoding'),
        );
    }

    /**
     * The case through the fluent chains, for the ops that have one.
     *
     * @param array<string, mixed> $case
     */
    private static function fluent(array $case): ?UrlChain
    {
        $cfg = Fixture::arr($case, 'config');
        $in = Fixture::arr($case, 'input');
        $source = Fixture::optStr($in, 'source') ?? '';
        $chain = match (Fixture::str($case, 'op')) {
            'imgproxy.url' => self::imgproxy($cfg)->image($source),
            'imgproxy.info_url' => self::imgproxy($cfg)->info($source),
            'imgproxy.video_url' => \array_key_exists('pdf_password', $in) ? null : self::imgproxy($cfg)->video($source),
            'query.url' => self::query($cfg)->image($source),
            'query.video_url' => self::query($cfg)->video($source),
            default => null,
        };
        if (null === $chain) {
            return null;
        }
        if ($chain instanceof \Xtandard\Image\QueryUrlChain) {
            foreach (Fixture::arr($in, 'params') as $p) {
                [$k, $v] = Fixture::pair($p);
                $chain->param($k, $v);
            }
        }
        foreach (Fixture::arr($in, 'options') as $i => $pipeline) {
            if ($i > 0) {
                if (!method_exists($chain, 'pipeline')) {
                    return null; // /info and /video chains cannot chain; the options form covers the error
                }
                $chain->pipeline();
            }
            foreach (\is_array($pipeline) ? $pipeline : [] as $entry) {
                if (!\is_array($entry) || !\is_string($entry[0] ?? null)) {
                    throw new \RuntimeException('bad option entry');
                }
                $chain->option($entry[0], ...Fixture::args($entry[1] ?? null));
            }
        }
        $enc = Fixture::optStr($in, 'source_encoding');
        if (null !== $enc && method_exists($chain, 'encoding')) {
            $chain->encoding($enc);
        }
        $ext = Fixture::optStr($in, 'extension');
        if (null !== $ext && method_exists($chain, 'extension')) {
            $chain->extension($ext);
        }
        $pdp = Fixture::optStr($in, 'pdf_password');
        if (null !== $pdp && method_exists($chain, 'withPdfPassword')) {
            $chain->withPdfPassword($pdp);
        }

        return $chain;
    }

    /**
     * The fixture's pipelines as a list of options arrays.
     *
     * @param array<mixed> $in
     *
     * @return list<array<string, list<string|int|float|bool|null>>>|null
     */
    private static function mappings(array $in): ?array
    {
        if (!\array_key_exists('options', $in)) {
            return null;
        }
        $out = [];
        foreach (Fixture::arr($in, 'options') as $pipeline) {
            $m = [];
            foreach (\is_array($pipeline) ? $pipeline : [] as $entry) {
                if (!\is_array($entry) || !\is_string($entry[0] ?? null)) {
                    throw new \RuntimeException('bad option entry');
                }
                $m[$entry[0]] = Fixture::args($entry[1] ?? null);
            }
            $out[] = $m;
        }

        return $out;
    }

    /**
     * The fixture's pipelines as Pipelines.
     *
     * @param array<mixed> $in
     *
     * @return Pipelines<array<string, list<string|int|float|bool|null>>>|null
     */
    private static function chain(array $in): ?Pipelines
    {
        $m = self::mappings($in);

        return null === $m ? null : new Pipelines(...$m);
    }

    /**
     * One options array, or the list when there are several (an error case).
     *
     * @param array<mixed> $in
     *
     * @return array<mixed>|null
     */
    private static function one(array $in): ?array
    {
        $m = self::mappings($in);
        if (null === $m) {
            return null;
        }

        return 1 === \count($m) ? $m[0] : $m;
    }

    /**
     * @param array<mixed> $in
     *
     * @return list<array{string, string|int|float|bool|null}>
     */
    private static function params(array $in): array
    {
        $out = [];
        foreach (Fixture::arr($in, 'params') as $p) {
            $out[] = Fixture::pair($p);
        }

        return $out;
    }

    /**
     * @param array<mixed> $in
     *
     * @return list<int>|null
     */
    private static function renditions(array $in): ?array
    {
        if (!\array_key_exists('renditions', $in)) {
            return null;
        }

        return array_map(static fn(mixed $r): int => \is_int($r) ? $r : throw new \RuntimeException('rendition'), array_values(Fixture::arr($in, 'renditions')));
    }

    private static function range(mixed $r): ByteRange|ByteSuffix|string
    {
        if (\is_string($r)) {
            return $r;
        }
        if (!\is_array($r)) {
            throw new \RuntimeException('bad range');
        }
        $suffix = Fixture::optInt($r, 'suffix');
        if (null !== $suffix) {
            return new ByteSuffix($suffix);
        }

        return new ByteRange(Fixture::optInt($r, 'start') ?? 0, Fixture::optInt($r, 'end'));
    }

    /** @param array<mixed> $cfg */
    private static function signing(array $cfg): ?SigningKey
    {
        $s = $cfg['signing'] ?? null;
        if (null === $s) {
            return null;
        }
        $k = true === $s ? Fixture::arr(Fixture::arr(Fixture::load(self::FILE), 'keys'), 'signing') : (\is_array($s) ? $s : []);
        $size = Fixture::optInt($cfg, 'signature_size');

        return null === $size
            ? new SigningKey(Fixture::str($k, 'key'), Fixture::str($k, 'salt'))
            : new SigningKey(Fixture::str($k, 'key'), Fixture::str($k, 'salt'), $size);
    }

    /** @param array<mixed> $cfg */
    private static function encryption(array $cfg): ?EncryptionKey
    {
        $e = $cfg['encryption'] ?? null;
        if (null === $e) {
            return null;
        }
        $k = true === $e ? Fixture::arr(Fixture::arr(Fixture::load(self::FILE), 'keys'), 'encryption') : (\is_array($e) ? $e : []);

        return new EncryptionKey(Fixture::str($k, 'key'), Fixture::optStr($k, 'iv'));
    }

    /** @param array<mixed> $cfg */
    private static function imgproxy(array $cfg): ImgproxyUrlBuilder
    {
        return new ImgproxyUrlBuilder(
            baseUrl: Fixture::optStr($cfg, 'base_url') ?? '',
            signing: self::signing($cfg),
            unsignedSignature: Fixture::optStr($cfg, 'unsigned_signature') ?? 'insecure',
            encryption: self::encryption($cfg),
            sourceEncoding: Fixture::optStr($cfg, 'source_encoding') ?? 'base64',
            splitSource: Fixture::optInt($cfg, 'split_source'),
            separator: Fixture::optStr($cfg, 'separator') ?? ':',
            optionNames: Fixture::optStr($cfg, 'option_names') ?? 'short',
        );
    }

    /** @param array<mixed> $cfg */
    private static function query(array $cfg): QueryUrlBuilder
    {
        return new QueryUrlBuilder(
            baseUrl: Fixture::optStr($cfg, 'base_url') ?? '',
            mount: Fixture::optStr($cfg, 'mount') ?? '/q',
            signing: self::signing($cfg),
            encryption: self::encryption($cfg),
            separator: Fixture::optStr($cfg, 'separator') ?? ':',
            optionNames: Fixture::optStr($cfg, 'option_names') ?? 'short',
        );
    }

    /**
     * @template T of object
     *
     * @param T|null $v
     *
     * @return T
     */
    private static function required(?object $v): object
    {
        self::assertNotNull($v);

        return $v;
    }
}
