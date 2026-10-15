<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Internal\WithExtension;
use Xtandard\Image\Internal\WithPdfPassword;
use Xtandard\Image\Internal\WithPipeline;

/**
 * A fluent /q/?url=… URL: the option methods write o=, param() a native key.
 *
 *     $q->image('s3://bucket/cat.jpg')->param('w', 300)->blur(2)->extension('webp')->build();
 *
 * @phpstan-import-type QueryValue from OptionTypes
 */
final class QueryUrlChain extends UrlChain
{
    use ProcessingMethods;
    use WithExtension;
    use WithPdfPassword;
    use WithPipeline;

    /**
     * Appends a native key (w, fit, g, …), written before o=; null is skipped.
     *
     * @param QueryValue|null $value
     */
    public function param(string $key, string|int|float|bool|null $value): static
    {
        $this->state->params[] = [$key, $value];

        return $this;
    }
}
