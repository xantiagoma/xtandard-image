<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Exception\UrlBuildException;

/**
 * Bytes $start to $end (inclusive), or to the end when $end is null.
 */
final readonly class ByteRange
{
    public function __construct(
        public int $start,
        public ?int $end = null,
    ) {}

    /**
     * Formats a byte range as a Range header value (bytes=0-1023, bytes=-500); a string is used as it is.
     *
     * @throws UrlBuildException invalid_range for a negative value or an end before the start
     */
    public static function header(self|ByteSuffix|string $range): string
    {
        if (\is_string($range)) {
            return $range;
        }
        if ($range instanceof ByteSuffix) {
            return 'bytes=-' . self::nonNegative($range->length, 'suffix');
        }
        $start = self::nonNegative($range->start, 'start');
        if (null === $range->end) {
            return "bytes={$start}-";
        }
        $end = self::nonNegative($range->end, 'end');
        if ($end < $start) {
            throw new UrlBuildException(ErrorCode::InvalidRange, "range end {$end} is before start {$start}");
        }

        return "bytes={$start}-{$end}";
    }

    /** @throws UrlBuildException */
    private static function nonNegative(int $n, string $what): int
    {
        if ($n < 0) {
            throw new UrlBuildException(ErrorCode::InvalidRange, "range {$what} {$n} is negative");
        }

        return $n;
    }
}
