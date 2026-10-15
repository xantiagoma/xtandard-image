<?php

declare(strict_types=1);

namespace Xtandard\Image;

/**
 * A query split by Signing::xiMessage(): its xi_sig value and the message xi_sig signs.
 */
final readonly class XiMessage
{
    /**
     * @param string|null $signature the xi_sig value, or null when the query has none
     * @param string $message the path, then "?" and the other parameters (no "?" when there are none)
     */
    public function __construct(
        public ?string $signature,
        public string $message,
    ) {}
}
