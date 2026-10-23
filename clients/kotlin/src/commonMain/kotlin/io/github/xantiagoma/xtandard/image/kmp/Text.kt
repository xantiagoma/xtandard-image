package io.github.xantiagoma.xtandard.image.kmp

import kotlin.io.encoding.Base64

/** Byte and text helpers shared by signing, encryption and the URL builders. */
internal object Text {
    private val extension = Regex(Constants.EXTENSION_PATTERN)
    private val base64Url = Base64.UrlSafe.withPadding(Base64.PaddingOption.ABSENT)
    private const val HEX = "0123456789ABCDEF"

    /** Decodes hex key material (either case, surrounding whitespace ignored). */
    fun hex(
        s: String,
        what: String,
    ): ByteArray {
        val t = s.trim()
        if (t.length % 2 != 0) fail(ErrorCode.INVALID_KEY, "$what is not a hex string")
        return ByteArray(t.length / 2) { i ->
            val hi = hexDigit(t[2 * i])
            val lo = hexDigit(t[2 * i + 1])
            if (hi < 0 || lo < 0) fail(ErrorCode.INVALID_KEY, "$what is not a hex string")
            ((hi shl 4) or lo).toByte()
        }
    }

    private fun hexDigit(c: Char): Int =
        when (c) {
            in '0'..'9' -> c - '0'
            in 'a'..'f' -> c - 'a' + 10
            in 'A'..'F' -> c - 'A' + 10
            else -> -1
        }

    /** Unpadded base64url (RFC 4648 section 5). */
    fun b64url(b: ByteArray): String = base64Url.encode(b)

    /**
     * UTF-8 bytes; lone surrogates become U+FFFD, as with JavaScript's TextEncoder. (`encodeToByteArray` writes `?`
     * for them on the JVM, so the encoding is spelled out.)
     */
    fun utf8(s: String): ByteArray {
        val out = ByteArray(s.length * 3)
        var n = 0
        var i = 0
        while (i < s.length) {
            val c = s[i]
            var cp = c.code
            if (c.isHighSurrogate() && i + 1 < s.length && s[i + 1].isLowSurrogate()) {
                cp = 0x10000 + ((c.code - 0xD800) shl 10) + (s[i + 1].code - 0xDC00)
                i++
            } else if (c.isSurrogate()) {
                cp = 0xFFFD
            }
            i++
            when {
                cp < 0x80 -> {
                    out[n++] = cp.toByte()
                }

                cp < 0x800 -> {
                    out[n++] = (0xC0 or (cp shr 6)).toByte()
                    out[n++] = (0x80 or (cp and 0x3F)).toByte()
                }

                cp < 0x10000 -> {
                    out[n++] = (0xE0 or (cp shr 12)).toByte()
                    out[n++] = (0x80 or ((cp shr 6) and 0x3F)).toByte()
                    out[n++] = (0x80 or (cp and 0x3F)).toByte()
                }

                else -> {
                    out[n++] = (0xF0 or (cp shr 18)).toByte()
                    out[n++] = (0x80 or ((cp shr 12) and 0x3F)).toByte()
                    out[n++] = (0x80 or ((cp shr 6) and 0x3F)).toByte()
                    out[n++] = (0x80 or (cp and 0x3F)).toByte()
                }
            }
        }
        return out.copyOf(n)
    }

    /** Whether s contains an ASCII control character (C0 or DEL). */
    fun hasControlChar(s: String): Boolean = s.any { it < ' ' || it == '\u007f' }

    /** Rejects an empty string or one with control characters. */
    fun checkText(
        s: String,
        code: ErrorCode,
        what: String,
    ): String {
        if (s.isEmpty() || hasControlChar(s)) fail(code, "$what must be a non-empty string without control characters")
        return s
    }

    /** Rejects an extension that is not alphanumeric. */
    fun checkExtension(ext: String): String {
        if (!extension.matches(ext)) fail(ErrorCode.INVALID_EXTENSION, "invalid extension \"$ext\"")
        return ext
    }

    /** Rejects a separator that is not one character or that the grammar uses. */
    fun checkSeparator(
        sep: String,
        forbidden: List<String>,
    ): String {
        val one = (sep.length == 1 && !sep[0].isSurrogate()) || (sep.length == 2 && sep[0].isHighSurrogate() && sep[1].isLowSurrogate())
        if (!one || sep in forbidden) fail(ErrorCode.INVALID_SEPARATOR, "invalid argument separator \"$sep\"")
        return sep
    }

    /** Percent-escapes a plain source: ASCII letters, digits and [Constants.PLAIN_SAFE] stay. */
    fun escapePlain(s: String): String = escape(s, Constants.PLAIN_SAFE)

    /** Percent-escapes a query key or value: ASCII letters, digits and [Constants.QUERY_SAFE] stay. */
    fun escapeQuery(s: String): String = escape(s, Constants.QUERY_SAFE)

    private fun escape(
        s: String,
        safe: String,
    ): String {
        val out = StringBuilder(s.length + 16)
        var i = 0
        while (i < s.length) {
            val c = s[i]
            val n = if (c.isHighSurrogate() && i + 1 < s.length && s[i + 1].isLowSurrogate()) 2 else 1
            if (n == 1 && c < '\u0080' && (c in 'a'..'z' || c in 'A'..'Z' || c in '0'..'9' || safe.indexOf(c) >= 0)) {
                out.append(c)
            } else {
                for (b in utf8(s.substring(i, i + n))) {
                    val v = b.toInt() and 0xff
                    out.append('%').append(HEX[v shr 4]).append(HEX[v and 0xf])
                }
            }
            i += n
        }
        return out.toString()
    }

    /** Whitespace as JavaScript's `\s` matches it (by code, so no invisible characters sit in the source). */
    fun isJsWhitespace(c: Char): Boolean {
        val x = c.code
        return x in 0x09..0x0D || x == 0x20 || x == 0xA0 || x == 0x1680 || x in 0x2000..0x200A || x == 0x2028 ||
            x == 0x2029 || x == 0x202F || x == 0x205F || x == 0x3000 || x == 0xFEFF
    }

    /**
     * Formats a number like ECMAScript `Number::toString` (300, 1.5, 0.1, 1e+21, 1e-7), so URLs and their
     * signatures match across languages. Integral types are written exactly; a Float is written as the shortest
     * decimal that reads back as that Float (1.1f is 1.1).
     */
    fun formatNumber(
        v: Number,
        where: String,
    ): String =
        when (v) {
            is Int, is Long, is Short, is Byte -> {
                v.toString()
            }

            is Float -> {
                if (!v.isFinite()) fail(ErrorCode.INVALID_ARGUMENT, "$where: $v is not a finite number")
                EcmaNumber.format(v.toDouble()) { it.toFloat() == v }
            }

            else -> {
                formatDouble(v.toDouble(), where)
            }
        }

    fun formatDouble(
        v: Double,
        where: String,
    ): String {
        if (!v.isFinite()) fail(ErrorCode.INVALID_ARGUMENT, "$where: $v is not a finite number")
        return EcmaNumber.format(v) { it.toDouble() == v }
    }
}

/**
 * ECMAScript `Number::toString` in common code: the fewest significant digits that read back as the same value
 * (closest to it when several do), then ECMAScript's choice of plain or exponent notation. Each double's exact
 * decimal value is computed with a small big-integer, so the result does not depend on the platform's own
 * `toString`.
 */
internal object EcmaNumber {
    private const val TWO_53 = 9007199254740992.0

    fun format(
        v: Double,
        readsBack: (String) -> Boolean,
    ): String {
        if (v == 0.0) return "0"
        val sign = if (v < 0) "-" else ""
        val a = kotlin.math.abs(v)
        if (a < TWO_53 && a == kotlin.math.floor(a)) return sign + a.toLong().toString()
        val (exact, n) = exactDigits(a)
        var best: Pair<String, Int>? = null
        for (p in 1..17) {
            if (p >= exact.length) {
                best = exact to n
                break
            }
            val (near, far) = round(exact, n, p)
            if (readsBack(sci(near))) {
                best = near
                break
            }
            if (readsBack(sci(far))) {
                best = far
                break
            }
        }
        val (digits0, n0) = best ?: (exact to n)
        val digits = digits0.trimEnd('0').ifEmpty { "0" }
        return sign + notation(digits, n0)
    }

    /** "d.ddde<n-1>": a form every platform's parser reads. */
    private fun sci(c: Pair<String, Int>): String {
        val (d, n) = c
        return "${d[0]}.${d.substring(1).ifEmpty { "0" }}E${n - 1}"
    }

    /** ECMAScript notation of 0.digits × 10^n. */
    private fun notation(
        digits: String,
        n: Int,
    ): String {
        val k = digits.length
        return when {
            n in k..21 -> {
                digits + "0".repeat(n - k)
            }

            n in 1..21 -> {
                digits.substring(0, n) + "." + digits.substring(n)
            }

            n in -5..0 -> {
                "0." + "0".repeat(-n) + digits
            }

            else -> {
                val e = n - 1
                val es = "e" + (if (e >= 0) "+" else "-") + kotlin.math.abs(e)
                if (k == 1) digits + es else digits[0] + "." + digits.substring(1) + es
            }
        }
    }

    /**
     * Rounds 0.exact × 10^n to p significant digits: the nearest (ties to even) and the neighbour on the other
     * side of the exact value.
     */
    private fun round(
        exact: String,
        n: Int,
        p: Int,
    ): Pair<Pair<String, Int>, Pair<String, Int>> {
        val kept = exact.substring(0, p)
        val next = exact[p]
        val restNonZero = exact.substring(p + 1).any { it != '0' }
        val up =
            when {
                next > '5' -> true
                next < '5' -> false
                restNonZero -> true
                else -> (kept.last() - '0') % 2 == 1
            }
        val down = kept to n
        val upped = increment(kept, n)
        return if (up) upped to down else down to upped
    }

    private fun increment(
        d: String,
        n: Int,
    ): Pair<String, Int> {
        val chars = d.toCharArray()
        var i = chars.size - 1
        while (i >= 0) {
            if (chars[i] == '9') {
                chars[i] = '0'
                i--
            } else {
                chars[i] = chars[i] + 1
                return chars.concatToString() to n
            }
        }
        return ("1" + chars.concatToString()).substring(0, d.length) to n + 1
    }

    /** The exact decimal digits of a positive finite double, as 0.digits × 10^n (no trailing zeros). */
    private fun exactDigits(a: Double): Pair<String, Int> {
        val bits = a.toRawBits()
        val biased = ((bits shr 52) and 0x7ff).toInt()
        val fraction = bits and ((1L shl 52) - 1)
        val m = if (biased == 0) fraction else fraction or (1L shl 52)
        val e = if (biased == 0) -1074 else biased - 1075
        val big = Limbs.of(m)
        val digits: String
        val n: Int
        if (e >= 0) {
            big.mulPow(2, e)
            digits = big.toDecimal()
            n = digits.length
        } else {
            big.mulPow(5, -e)
            digits = big.toDecimal()
            n = digits.length + e
        }
        return digits.trimEnd('0') to n
    }

    /** A non-negative big integer in base 10^9 limbs, least significant first. */
    private class Limbs private constructor(
        private var limbs: IntArray,
        private var size: Int,
    ) {
        fun mulSmall(f: Long) {
            var carry = 0L
            for (i in 0 until size) {
                val x = limbs[i] * f + carry
                limbs[i] = (x % BASE).toInt()
                carry = x / BASE
            }
            while (carry > 0) {
                if (size == limbs.size) limbs = limbs.copyOf(limbs.size * 2)
                limbs[size++] = (carry % BASE).toInt()
                carry /= BASE
            }
        }

        fun mulPow(
            b: Int,
            times: Int,
        ) {
            // 2^29 and 5^12 keep limb × factor + carry inside a Long.
            val step = if (b == 2) 29 else 12
            val chunk = if (b == 2) 1L shl 29 else 244140625L
            var left = times
            while (left >= step) {
                mulSmall(chunk)
                left -= step
            }
            var rest = 1L
            repeat(left) { rest *= b }
            if (rest > 1) mulSmall(rest)
        }

        fun toDecimal(): String {
            val sb = StringBuilder()
            sb.append(limbs[size - 1])
            for (i in size - 2 downTo 0) sb.append(limbs[i].toString().padStart(9, '0'))
            return sb.toString()
        }

        companion object {
            const val BASE = 1_000_000_000L

            fun of(v: Long): Limbs {
                val l = Limbs(IntArray(8), 0)
                var x = v
                if (x == 0L) {
                    l.limbs[0] = 0
                    l.size = 1
                }
                while (x > 0) {
                    l.limbs[l.size++] = (x % BASE).toInt()
                    x /= BASE
                }
                return l
            }
        }
    }
}
