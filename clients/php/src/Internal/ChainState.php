<?php

declare(strict_types=1);

namespace Xtandard\Image\Internal;

use Xtandard\Image\SourceEncoding;

/**
 * What a fluent chain collected.
 *
 * @internal
 *
 * @phpstan-import-type Entry from Options
 */
final class ChainState
{
    /** @var non-empty-list<list<Entry>> */
    public array $pipelines = [[]];

    /** @var list<array{string, string|int|float|bool|null}> */
    public array $params = [];

    public ?SourceEncoding $encoding = null;

    public ?string $extension = null;

    public ?string $pdfPassword = null;

    public function __construct(public readonly string $source) {}

    /** @param Entry $entry */
    public function add(array $entry): void
    {
        $last = \count($this->pipelines) - 1;
        $this->pipelines[$last][] = $entry;
    }
}
