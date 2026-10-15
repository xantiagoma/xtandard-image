<?php

declare(strict_types=1);

namespace Xtandard\Image\Tests;

/**
 * Loads the shared fixtures from the repository (clients/php/tests → repository root).
 */
final class Fixture
{
    /** @var array<string, array<mixed>> */
    private static array $cache = [];

    /** @return array<mixed> */
    public static function load(string $rel): array
    {
        if (!isset(self::$cache[$rel])) {
            $text = file_get_contents(\dirname(__DIR__, 3) . '/' . $rel);
            if (false === $text) {
                throw new \RuntimeException("cannot read {$rel}");
            }
            $data = json_decode($text, true, 512, \JSON_THROW_ON_ERROR);
            if (!\is_array($data)) {
                throw new \RuntimeException("{$rel} is not a JSON object");
            }
            self::$cache[$rel] = $data;
        }

        return self::$cache[$rel];
    }

    /**
     * A list of objects of a fixture, as data provider rows keyed by name.
     *
     * @return array<string, array{array<string, mixed>}>
     */
    public static function rows(string $rel, string $key, ?string $prefixKey = null): array
    {
        $list = self::load($rel)[$key] ?? null;
        if (!\is_array($list)) {
            throw new \RuntimeException("{$rel} has no {$key}");
        }
        $rows = [];
        foreach ($list as $item) {
            if (!\is_array($item)) {
                throw new \RuntimeException("{$rel} {$key}: not an object");
            }
            /** @var array<string, mixed> $item */
            $name = self::str($item, 'name');
            if (null !== $prefixKey) {
                $name = self::str($item, $prefixKey) . ':' . $name;
            }
            $rows[$name] = [$item];
        }

        return $rows;
    }

    /** @param array<mixed> $a */
    public static function str(array $a, string $key): string
    {
        $v = $a[$key] ?? null;
        if (!\is_string($v)) {
            throw new \RuntimeException("{$key} is not a string");
        }

        return $v;
    }

    /** @param array<mixed> $a */
    public static function optStr(array $a, string $key): ?string
    {
        $v = $a[$key] ?? null;
        if (null !== $v && !\is_string($v)) {
            throw new \RuntimeException("{$key} is not a string");
        }

        return $v;
    }

    /** @param array<mixed> $a */
    public static function optInt(array $a, string $key): ?int
    {
        $v = $a[$key] ?? null;
        if (null !== $v && !\is_int($v)) {
            throw new \RuntimeException("{$key} is not an integer");
        }

        return $v;
    }

    /**
     * @param array<mixed> $a
     *
     * @return array<mixed>
     */
    public static function arr(array $a, string $key): array
    {
        $v = $a[$key] ?? [];
        if (!\is_array($v)) {
            throw new \RuntimeException("{$key} is not an array");
        }

        return $v;
    }

    /**
     * An argument list of the fixture as typed arguments.
     *
     * @return list<string|int|float|bool|null>
     */
    public static function args(mixed $args): array
    {
        if (!\is_array($args)) {
            throw new \RuntimeException('arguments are not a list');
        }
        $out = [];
        foreach ($args as $a) {
            if (null !== $a && !\is_scalar($a)) {
                throw new \RuntimeException('argument is not a scalar');
            }
            $out[] = $a;
        }

        return $out;
    }

    /**
     * A [key, value] pair of the fixture.
     *
     * @return array{string, string|int|float|bool|null}
     */
    public static function pair(mixed $p): array
    {
        $kv = self::args($p);
        if (2 !== \count($kv) || !\is_string($kv[0])) {
            throw new \RuntimeException('not a [key, value] pair');
        }

        return [$kv[0], $kv[1]];
    }
}
