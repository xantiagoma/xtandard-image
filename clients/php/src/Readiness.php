<?php

declare(strict_types=1);

namespace Xtandard\Image;

/**
 * The /readyz answer: $ready is true only for 200 "ready".
 */
final readonly class Readiness
{
    /**
     * @param 'ready'|'starting'|'draining' $state
     */
    public function __construct(
        public bool $ready,
        public string $state,
    ) {}
}
