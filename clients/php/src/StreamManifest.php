<?php

declare(strict_types=1);

namespace Xtandard\Image;

/**
 * A fetched HLS playlist or DASH MPD.
 */
final readonly class StreamManifest
{
    /**
     * @param string $url the (signed) manifest URL; hand it to a player
     * @param string $contentType application/vnd.apple.mpegurl or application/dash+xml
     * @param string $body the manifest; its child URLs (/_stream/<ticket>/…) carry a ticket and need no signing
     */
    public function __construct(
        public string $url,
        public string $contentType,
        public string $body,
    ) {}
}
