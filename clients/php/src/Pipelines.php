<?php

declare(strict_types=1);

namespace Xtandard\Image;

/**
 * Chained pipelines (/-/) for the options parameter of url(), and the ordered
 * option groups of QueryUrlBuilder::videoUrl() (where keys may repeat):
 *
 *     $img->url($source, new Pipelines(['rs' => ['fit', 500, 500]], ['bl' => 2]));
 *
 * A plain list of options arrays works at run time too, but static analysers
 * check each pipeline's shape only through this class (PHPStan merges a union
 * of an array shape and a list of shapes into a loose array type).
 *
 * @template-covariant T of array<mixed>
 */
final readonly class Pipelines
{
    /** @var list<T> */
    public array $pipelines;

    /** @param T ...$pipelines one options array per pipeline, in order */
    public function __construct(array ...$pipelines)
    {
        $this->pipelines = array_values($pipelines);
    }
}
