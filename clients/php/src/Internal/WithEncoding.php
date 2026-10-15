<?php

declare(strict_types=1);

namespace Xtandard\Image\Internal;

use Xtandard\Image\SourceEncoding;

/**
 * encoding(): the source encoding of one URL.
 *
 * @internal
 */
trait WithEncoding
{
    /**
     * Overrides the source encoding.
     *
     * @param SourceEncoding|value-of<SourceEncoding> $encoding
     *
     * @throws \Xtandard\Image\Exception\UrlBuildException invalid_request for an unknown encoding
     */
    public function encoding(SourceEncoding|string $encoding): static
    {
        $this->state->encoding = Streams::encoding($encoding);

        return $this;
    }
}
