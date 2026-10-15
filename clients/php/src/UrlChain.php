<?php

declare(strict_types=1);

namespace Xtandard\Image;

use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\Internal\ChainState;
use Xtandard\Image\Internal\Options;

/**
 * Shared state and control methods of the fluent URL builders. Every method
 * but build() returns the same chain; build() (or a string cast) makes the URL.
 */
abstract class UrlChain implements \Stringable
{
    /** @internal */
    protected readonly ChainState $state;

    /**
     * @param \Closure(ChainState): string $run
     *
     * @internal start a chain from a builder: image(), info(), video()
     */
    final public function __construct(private readonly \Closure $run, string $source)
    {
        $this->state = new ChainState($source);
    }

    /** Appends one option occurrence (as the per-option methods do), by name or alias. */
    public function option(string $name, string|int|float|bool|null ...$args): static
    {
        $this->state->add([$name, array_values($args)]);

        return $this;
    }

    /**
     * Appends every option of an options array, in key order.
     *
     * @param array<string, mixed> $options
     *
     * @throws UrlBuildException invalid_argument for an unsupported argument type
     */
    public function options(array $options): static
    {
        foreach (Options::entries($options) as $e) {
            $this->state->add($e);
        }

        return $this;
    }

    /**
     * Builds the URL.
     *
     * @throws UrlBuildException
     */
    public function build(): string
    {
        return ($this->run)($this->state);
    }

    /**
     * The URL; a string cast throws like build().
     *
     * @throws UrlBuildException
     */
    public function __toString(): string
    {
        return $this->build();
    }
}
