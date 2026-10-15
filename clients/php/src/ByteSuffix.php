<?php

declare(strict_types=1);

namespace Xtandard\Image;

/**
 * The last $length bytes.
 */
final readonly class ByteSuffix
{
    public function __construct(public int $length) {}
}
