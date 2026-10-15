<?php

declare(strict_types=1);

namespace Xtandard\Image\Internal;

use Xtandard\Image\ErrorCode;
use Xtandard\Image\Exception\UrlBuildException;
use Xtandard\Image\OptionNames;
use Xtandard\Image\OptionNameStyle;
use Xtandard\Image\Pipelines;

/**
 * Option catalogues at run time and their serialisation into option segments.
 *
 * An entry is one option occurrence: a name (canonical or alias) and its arguments.
 *
 * @internal
 *
 * @phpstan-type Arg string|int|float|bool|null
 * @phpstan-type Entry array{string, list<Arg>}
 */
final class Options
{
    /** Characters an option argument cannot contain besides the separator (ECMAScript \s, C0 and DEL). */
    private const BAD_ARG = '/[\/?#\x00-\x20\x7f\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]/u';

    /**
     * @param array<string, string> $names name or alias to canonical name
     * @param array<string, string> $short canonical name to short name
     */
    private function __construct(
        public readonly string $kind,
        private readonly array $names,
        private readonly array $short,
    ) {}

    private static ?self $processing = null;

    private static ?self $info = null;

    private static ?self $video = null;

    public static function processing(): self
    {
        return self::$processing ??= new self('imgproxy', OptionNames::PROCESSING_NAMES, OptionNames::PROCESSING_SHORT);
    }

    public static function info(): self
    {
        return self::$info ??= new self('info', OptionNames::INFO_NAMES, OptionNames::INFO_SHORT);
    }

    public static function video(): self
    {
        return self::$video ??= new self('video', OptionNames::VIDEO_NAMES, OptionNames::VIDEO_SHORT);
    }

    /** The canonical name of a name or alias, or null. */
    public function canonical(string $name): ?string
    {
        return $this->names[$name] ?? null;
    }

    /**
     * The name an option is written with.
     *
     * @throws UrlBuildException unknown_option
     */
    public function writtenName(string $name, OptionNameStyle $style): string
    {
        $canonical = $this->names[$name] ?? null;
        if (null === $canonical) {
            throw new UrlBuildException(ErrorCode::UnknownOption, \sprintf('unknown %s option %s', $this->kind, var_export($name, true)));
        }

        return match ($style) {
            OptionNameStyle::Given => $name,
            OptionNameStyle::Canonical => $canonical,
            OptionNameStyle::Short => $this->short[$canonical] ?? $canonical,
        };
    }

    /**
     * Formats one option segment (rs:fill:300:400).
     *
     * @param Entry $entry
     *
     * @throws UrlBuildException
     */
    public function formatEntry(array $entry, string $sep, OptionNameStyle $style): string
    {
        [$name, $args] = $entry;
        $parts = [$this->writtenName($name, $style)];
        if ([] === $args) {
            throw new UrlBuildException(ErrorCode::InvalidArgument, "option {$name}: at least one argument is required");
        }
        foreach ($args as $i => $a) {
            $parts[] = self::formatArg($a, $sep, "option {$name} argument " . ($i + 1));
        }

        return implode($sep, $parts);
    }

    /**
     * Formats one argument. Strings are checked, never escaped: the server does
     * not unescape option arguments.
     *
     * @param Arg $a
     *
     * @throws UrlBuildException invalid_argument
     */
    public static function formatArg(string|int|float|bool|null $a, string $sep, string $where): string
    {
        if (null === $a) {
            return '';
        }
        if (\is_bool($a)) {
            return $a ? '1' : '0';
        }
        if (!\is_string($a)) {
            return Text::formatNumber($a, $where);
        }
        if (str_contains($a, $sep) || 1 === preg_match(self::BAD_ARG, $a) || false === preg_match(self::BAD_ARG, $a)) {
            throw new UrlBuildException(ErrorCode::InvalidArgument, \sprintf(
                '%s: argument %s contains a character that cannot appear in an option',
                $where,
                var_export($a, true),
            ));
        }

        return $a;
    }

    /**
     * Converts an options array (one pipeline) into entries, in key order; null values are skipped.
     *
     * @param array<mixed> $options
     *
     * @return list<Entry>
     *
     * @throws UrlBuildException invalid_argument for an unsupported argument type
     */
    public static function entries(array $options): array
    {
        $out = [];
        foreach ($options as $name => $value) {
            $name = (string) $name;
            if (null === $value) {
                continue;
            }
            $args = \is_array($value) ? $value : [$value];
            $list = [];
            foreach ($args as $a) {
                $list[] = self::asArg($name, $a);
            }
            $out[] = [$name, $list];
        }

        return $out;
    }

    /**
     * One entry list per pipeline: an options array is one pipeline; Pipelines,
     * or a list of options arrays, chains them.
     *
     * @param array<mixed>|Pipelines<array<mixed>>|null $options
     *
     * @return non-empty-list<list<Entry>>
     *
     * @throws UrlBuildException
     */
    public static function pipelines(array|Pipelines|null $options): array
    {
        if ($options instanceof Pipelines) {
            $options = $options->pipelines;
        }
        if (null === $options || [] === $options) {
            return [[]];
        }
        if (!array_is_list($options)) {
            return [self::entries($options)];
        }
        $out = [];
        foreach ($options as $i => $p) {
            if (!\is_array($p)) {
                throw new UrlBuildException(ErrorCode::InvalidArgument, "pipeline {$i} is not an options array");
            }
            $out[] = self::entries($p);
        }

        return $out;
    }

    /**
     * @return Arg
     *
     * @throws UrlBuildException invalid_argument
     */
    private static function asArg(string $name, mixed $a): string|int|float|bool|null
    {
        if (null === $a || \is_string($a) || \is_int($a) || \is_float($a) || \is_bool($a)) {
            return $a;
        }
        if ($a instanceof \BackedEnum) {
            return $a->value;
        }

        throw new UrlBuildException(ErrorCode::InvalidArgument, "option {$name}: unsupported argument type " . get_debug_type($a));
    }
}
