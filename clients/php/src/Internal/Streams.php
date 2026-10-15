<?php

declare(strict_types=1);

namespace Xtandard\Image\Internal;

use Xtandard\Image\Constants;
use Xtandard\Image\ErrorCode;
use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\SegmentFormat;
use Xtandard\Image\SourceEncoding;
use Xtandard\Image\StreamProtocol;

/**
 * Stream settings and the enum arguments shared by the builders.
 *
 * @internal
 *
 * @phpstan-import-type Entry from Options
 */
final class Streams
{
    /** Video options a stream request sets itself. */
    private const RESERVED = ['stream', 'segment_duration', 'segment_format', 'format'];

    private function __construct() {}

    /**
     * The video options and manifest container of a stream request.
     *
     * @param list<list<Entry>> $pipelines
     * @param list<int>|null $renditions
     *
     * @return array{list<Entry>, string}
     *
     * @throws UrlBuildException
     */
    public static function parts(
        array $pipelines,
        StreamProtocol|string $protocol,
        ?array $renditions,
        ?int $segmentDuration,
        SegmentFormat|string|null $segmentFormat,
    ): array {
        $entries = array_merge(...$pipelines);
        $video = Options::video();
        foreach ($entries as [$name]) {
            if (\in_array($video->canonical($name), self::RESERVED, true)) {
                throw new UrlBuildException(ErrorCode::InvalidRequest, "stream requests set {$name} through protocol, renditions and the segment settings");
            }
        }
        $p = $protocol instanceof StreamProtocol ? $protocol : StreamProtocol::tryFrom($protocol);
        if (null === $p) {
            throw new UrlBuildException(ErrorCode::InvalidRequest, \sprintf('unknown stream protocol %s', var_export($protocol, true)));
        }
        if (null !== $renditions && [] !== $renditions) {
            $entries[] = ['stream', [$p->value, ...$renditions]];
        }
        if (null !== $segmentDuration) {
            $entries[] = ['segment_duration', [$segmentDuration]];
        }
        if (null !== $segmentFormat) {
            $f = $segmentFormat instanceof SegmentFormat ? $segmentFormat->value : $segmentFormat;
            $protocols = Constants::SEGMENT_FORMAT_PROTOCOLS[$f] ?? null;
            if (null === $protocols || !\in_array($p->value, $protocols, true)) {
                throw new UrlBuildException(ErrorCode::InvalidRequest, "segment format {$f} is not available for {$p->value}");
            }
            if (Constants::DEFAULT_SEGMENT_FORMAT !== $f) {
                $entries[] = ['segment_format', [$f]];
            }
        }

        return [$entries, $p->container()];
    }

    /**
     * A source encoding from its enum or its name.
     *
     * @throws UrlBuildException invalid_request for an unknown encoding
     */
    public static function encoding(SourceEncoding|string $encoding): SourceEncoding
    {
        $e = $encoding instanceof SourceEncoding ? $encoding : SourceEncoding::tryFrom($encoding);
        if (null === $e) {
            throw new UrlBuildException(ErrorCode::InvalidRequest, \sprintf('unknown source encoding %s', var_export($encoding, true)));
        }

        return $e;
    }

    /** The extension string of an enum or a string. */
    public static function extension(\BackedEnum|string|null $ext): ?string
    {
        return $ext instanceof \BackedEnum ? (string) $ext->value : $ext;
    }

    /**
     * Rejects a separator that is not one character or that the grammar uses.
     *
     * @param list<string> $forbidden
     *
     * @throws UrlBuildException invalid_separator
     */
    public static function separator(string $sep, array $forbidden): string
    {
        if (1 !== preg_match('/^.$/sDu', $sep) || \in_array($sep, $forbidden, true)) {
            throw new UrlBuildException(ErrorCode::InvalidSeparator, \sprintf('invalid argument separator %s', var_export($sep, true)));
        }

        return $sep;
    }

    /**
     * The pdf_password token, checking that both keys are present (encryption first).
     *
     * @throws UrlBuildException
     */
    public static function passwordToken(?\Xtandard\Image\EncryptionKey $encryption, ?\Xtandard\Image\SigningKey $signing, #[\SensitiveParameter] string $password): string
    {
        if (null === $encryption) {
            throw new UrlBuildException(ErrorCode::MissingEncryption, 'pdf_password needs an encryption key');
        }
        if (null === $signing) {
            throw new UrlBuildException(ErrorCode::MissingSigning, 'pdf_password needs signing: the server refuses it on unsigned URLs');
        }

        return \Xtandard\Image\Encryption::encryptPdfPassword($password, $encryption);
    }
}
