<?php

declare(strict_types=1);

namespace Xtandard\Image\Internal;

use Xtandard\Image\Constants;
use Xtandard\Image\ErrorCode;
use Xtandard\Image\Exception\UrlBuildException;

/**
 * Byte and text helpers shared by signing, encryption and the URL builders.
 *
 * @internal
 */
final class Text
{
    private function __construct() {}

    /** Decodes hex key material (either case, surrounding whitespace ignored). */
    public static function hexBytes(#[\SensitiveParameter] string $hex, string $what): string
    {
        $s = trim($hex);
        if (0 !== \strlen($s) % 2 || 1 !== preg_match('/^[0-9a-fA-F]*$/D', $s)) {
            throw new UrlBuildException(ErrorCode::InvalidKey, "{$what} is not a hex string");
        }
        $bytes = hex2bin($s);
        if (false === $bytes) {
            throw new UrlBuildException(ErrorCode::InvalidKey, "{$what} is not a hex string");
        }

        return $bytes;
    }

    /** Unpadded base64url (RFC 4648 §5). */
    public static function b64url(string $bytes): string
    {
        return rtrim(strtr(base64_encode($bytes), '+/', '-_'), '=');
    }

    /** Whether $s contains an ASCII control character (C0 or DEL). */
    public static function hasControlChar(string $s): bool
    {
        return 1 === preg_match('/[\x00-\x1f\x7f]/', $s);
    }

    /** Rejects an empty string or one with control characters. */
    public static function checkText(string $s, ErrorCode $code, string $what): string
    {
        if ('' === $s || self::hasControlChar($s)) {
            throw new UrlBuildException($code, "{$what} must be a non-empty string without control characters");
        }

        return $s;
    }

    /** Rejects an extension that is not alphanumeric. */
    public static function checkExtension(string $ext): string
    {
        if (1 !== preg_match('/' . Constants::EXTENSION_PATTERN . '/D', $ext)) {
            throw new UrlBuildException(ErrorCode::InvalidExtension, \sprintf('invalid extension %s', var_export($ext, true)));
        }

        return $ext;
    }

    /**
     * Percent-escapes a source for a plain/ URL: ASCII letters, digits and
     * Constants::PLAIN_SAFE are kept, every other byte of the UTF-8 string is %XX.
     */
    public static function escapePlainSource(string $source): string
    {
        return self::escape($source, Constants::PLAIN_SAFE);
    }

    /** Percent-escapes a query key or value, keeping letters, digits and Constants::QUERY_SAFE. */
    public static function escapeQueryValue(string $value): string
    {
        return self::escape($value, Constants::QUERY_SAFE);
    }

    private static function escape(string $s, string $safe): string
    {
        $out = '';
        $n = \strlen($s);
        for ($i = 0; $i < $n; ++$i) {
            $c = $s[$i];
            $o = \ord($c);
            $alnum = ($o >= 0x30 && $o <= 0x39) || ($o >= 0x41 && $o <= 0x5A) || ($o >= 0x61 && $o <= 0x7A);
            $out .= $alnum || str_contains($safe, $c) ? $c : \sprintf('%%%02X', $o);
        }

        return $out;
    }

    /**
     * Formats a number like ECMAScript Number::toString (300, 1.5, 0.1, 1e+21, 1e-7).
     *
     * Every client writes numbers this way, so URLs and their signatures match
     * across languages. Integers are written as they are.
     */
    public static function formatNumber(int|float $v, string $where): string
    {
        if (\is_int($v)) {
            return (string) $v;
        }
        if (!is_finite($v)) {
            throw new UrlBuildException(ErrorCode::InvalidArgument, "{$where}: {$v} is not a finite number");
        }
        if (0.0 === $v) {
            return '0';
        }
        $sign = $v < 0 ? '-' : '';
        $a = abs($v);
        // The shortest decimal that round-trips: d.ddd × 10^e.
        $repr = '';
        for ($p = 0; $p < 17; ++$p) {
            $repr = \sprintf('%.' . $p . 'e', $a);
            if ((float) $repr === $a) {
                break;
            }
        }
        $at = (int) strpos($repr, 'e');
        $digits = rtrim(str_replace('.', '', substr($repr, 0, $at)), '0');
        $exp = substr($repr, $at + 1);
        // abs(v) = 0.<digits> × 10^n (ECMAScript's k digits and exponent n).
        $n = (int) $exp + 1;
        $k = \strlen($digits);
        if ($k <= $n && $n <= 21) {
            $out = $digits . str_repeat('0', $n - $k);
        } elseif (0 < $n && $n <= 21) {
            $out = substr($digits, 0, $n) . '.' . substr($digits, $n);
        } elseif (-6 < $n && $n <= 0) {
            $out = '0.' . str_repeat('0', -$n) . $digits;
        } else {
            $e = $n - 1;
            $es = 'e' . ($e >= 0 ? '+' : '-') . abs($e);
            $out = 1 === $k ? $digits . $es : $digits[0] . '.' . substr($digits, 1) . $es;
        }

        return $sign . $out;
    }
}
