<?php

declare(strict_types=1);

namespace Xtandard\Image\Tests;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Xtandard\Image\EncryptionKey;
use Xtandard\Image\ImgproxyUrlBuilder;
use Xtandard\Image\OptionNameStyle;
use Xtandard\Image\QueryUrlBuilder;
use Xtandard\Image\SigningKey;
use Xtandard\Image\UrlChain;

/**
 * testdata/urls/imgproxy-builder.json: 400+ cases derived from the server's golden tables.
 *
 * The URLs were written by the TypeScript client and parsed back by the Go
 * dialect tests (TestBuilderFixture); every client must write the same ones:
 * url with option names as given, url_short with the default short names, and
 * query_url (query dialect) where the case has one.
 */
final class BuilderFixtureTest extends TestCase
{
    private const FILE = 'testdata/urls/imgproxy-builder.json';

    /** @return array<string, array{array<string, mixed>}> */
    public static function cases(): array
    {
        return Fixture::rows(self::FILE, 'cases');
    }

    public function testCoversTheTables(): void
    {
        self::assertGreaterThanOrEqual(400, \count(self::cases()));
    }

    /** @param array<string, mixed> $case */
    #[DataProvider('cases')]
    public function testCase(array $case): void
    {
        self::assertSame(Fixture::str($case, 'url'), $this->fluent($case, OptionNameStyle::Given));
        self::assertSame(Fixture::str($case, 'url_short'), $this->fluent($case, OptionNameStyle::Short));
        if (true === ($case['query'] ?? false)) {
            self::assertSame(Fixture::str($case, 'query_url'), $this->query($case));
        }
    }

    /** @param array<string, mixed> $case */
    private function fluent(array $case, OptionNameStyle $style): string
    {
        $enc = Fixture::arr(Fixture::load(self::FILE), 'encryption');
        $b = new ImgproxyUrlBuilder(
            signing: self::signing($case),
            unsignedSignature: 'unsafe',
            encryption: new EncryptionKey(Fixture::str($enc, 'key'), Fixture::str($enc, 'iv')),
            separator: Fixture::optStr($case, 'separator') ?? ':',
            optionNames: $style,
        );
        $source = Fixture::str($case, 'source');
        $chain = match (Fixture::optStr($case, 'endpoint')) {
            'info' => $b->info($source),
            'video' => $b->video($source),
            default => $b->image($source),
        };
        $chain->encoding(Fixture::str($case, 'encoding'));
        self::apply($chain, $case);

        return $chain->build();
    }

    /** @param array<string, mixed> $case */
    private function query(array $case): string
    {
        $q = new QueryUrlBuilder(signing: self::signing($case));
        $source = Fixture::str($case, 'source');
        $chain = 'video' === Fixture::optStr($case, 'endpoint') ? $q->video($source) : $q->image($source);
        self::apply($chain, $case);

        return $chain->build();
    }

    /** @param array<string, mixed> $case */
    private static function apply(UrlChain $chain, array $case): void
    {
        foreach (Fixture::arr($case, 'pipelines') as $i => $pipeline) {
            if ($i > 0) {
                self::assertTrue(method_exists($chain, 'pipeline'), 'chained pipelines need pipeline()');
                $chain->pipeline();
            }
            foreach (\is_array($pipeline) ? $pipeline : [] as $entry) {
                foreach (\is_array($entry) ? $entry : [] as $name => $args) {
                    $chain->option((string) $name, ...Fixture::args($args));
                }
            }
        }
        $ext = Fixture::optStr($case, 'extension');
        if (null !== $ext && '' !== $ext && method_exists($chain, 'extension')) {
            $chain->extension($ext);
        }
    }

    /** @param array<string, mixed> $case */
    private static function signing(array $case): ?SigningKey
    {
        $size = Fixture::optInt($case, 'signature_size');
        if (null === $size || 0 === $size) {
            return null;
        }
        $s = Fixture::arr(Fixture::load(self::FILE), 'signing');

        return new SigningKey(Fixture::str($s, 'key'), Fixture::str($s, 'salt'), $size);
    }
}
