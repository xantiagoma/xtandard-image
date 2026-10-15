<?php

declare(strict_types=1);

namespace Xtandard\Image\Internal;

/**
 * extension(): the output format or container.
 *
 * @internal
 */
trait WithExtension
{
    /** Sets the output extension (@webp / .webp; a container for /video). */
    public function extension(\BackedEnum|string $extension): static
    {
        $this->state->extension = Streams::extension($extension);

        return $this;
    }
}
