<?php

declare(strict_types=1);

namespace Xtandard\Image\Internal;

/**
 * pipeline(): chained pipelines.
 *
 * @internal
 */
trait WithPipeline
{
    /** Starts the next chained pipeline (/-/). */
    public function pipeline(): static
    {
        $this->state->pipelines[] = [];

        return $this;
    }
}
