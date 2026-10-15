"""Byte and text helpers shared by signing, encryption and the URL builders."""

from __future__ import annotations

import base64
import math
import re
from typing import TYPE_CHECKING

from ._generated.constants import EXTENSION_PATTERN, PLAIN_SAFE, QUERY_SAFE
from .errors import ErrorCode, URLBuildError

if TYPE_CHECKING:
    from collections.abc import Callable

KeyMaterial = str | bytes
"""Key material: a hex string (as in XI_SIGNING_KEYS) or raw bytes."""

_HEX = re.compile(r"[0-9a-fA-F]*")
_CONTROL = re.compile(r"[\x00-\x1f\x7f]")
_EXTENSION = re.compile(EXTENSION_PATTERN)
_PLAIN_KEEP = re.compile(f"[A-Za-z0-9{re.escape(PLAIN_SAFE)}]")
_QUERY_KEEP = re.compile(f"[A-Za-z0-9{re.escape(QUERY_SAFE)}]")


def key_bytes(k: KeyMaterial, what: str) -> bytes:
    """Decodes hex key material (either case, surrounding whitespace ignored) or copies raw bytes."""
    if isinstance(k, bytes | bytearray | memoryview):
        return bytes(k)
    s = k.strip()
    if len(s) % 2 != 0 or not _HEX.fullmatch(s):
        raise URLBuildError("invalid_key", f"{what} is not a hex string")
    return bytes.fromhex(s)


def b64url(b: bytes) -> str:
    """Unpadded base64url (RFC 4648 §5)."""
    return base64.urlsafe_b64encode(b).rstrip(b"=").decode("ascii")


def has_control_char(s: str) -> bool:
    """Reports whether s contains an ASCII control character (C0 or DEL)."""
    return _CONTROL.search(s) is not None


def check_text(s: str, code: ErrorCode, what: str) -> str:
    """Rejects an empty string or one with control characters."""
    if s == "" or has_control_char(s):
        raise URLBuildError(code, f"{what} must be a non-empty string without control characters")
    return s


def check_extension(ext: str) -> str:
    """Rejects an extension that is not alphanumeric."""
    if not _EXTENSION.fullmatch(ext):
        raise URLBuildError("invalid_extension", f"invalid extension {ext!r}")
    return ext


def utf8(s: str) -> bytes:
    """UTF-8 bytes of s; lone surrogates become U+FFFD, as with TextEncoder."""
    try:
        return s.encode("utf-8")
    except UnicodeEncodeError:
        return "".join(chr(0xFFFD) if 0xD800 <= ord(c) <= 0xDFFF else c for c in s).encode("utf-8")


def _escape(s: str, keep: Callable[[str], bool]) -> str:
    out: list[str] = []
    for ch in s:
        if keep(ch):
            out.append(ch)
        else:
            out.extend(f"%{b:02X}" for b in utf8(ch))
    return "".join(out)


def escape_plain_source(source: str) -> str:
    """Percent-escapes a source for a ``plain/`` URL; the server path-unescapes it.

    RFC 3986 unreserved characters, the sub-delims, ``:`` and ``/`` are kept;
    everything else (``%``, ``@``, ``?``, ``#``, spaces, non-ASCII) is
    percent-encoded as UTF-8.
    """
    return _escape(source, lambda ch: _PLAIN_KEEP.fullmatch(ch) is not None)


def escape_query_value(value: str) -> str:
    """Percent-escapes a query key or value, keeping ``:``, ``/`` and ``,`` readable."""
    return _escape(value, lambda ch: _QUERY_KEEP.fullmatch(ch) is not None)


def format_number(v: float, where: str) -> str:
    """Formats a number like ECMAScript Number::toString (300, 1.5, 0.1, 1e+21, 1e-7).

    Every client writes numbers this way, so URLs (and their signatures)
    match across languages.
    """
    if isinstance(v, int):
        if abs(v) < 10**21:
            return str(v)
        v = float(v)
    if not math.isfinite(v):
        raise URLBuildError("invalid_argument", f"{where}: {v} is not a finite number")
    if v == 0:
        return "0"
    sign = "-" if v < 0 else ""
    mantissa, _, exp = repr(abs(v)).partition("e")
    int_part, _, frac = mantissa.partition(".")
    all_digits = int_part + frac
    lead = len(all_digits) - len(all_digits.lstrip("0"))
    digits = all_digits[lead:].rstrip("0")
    # abs(v) = 0.<digits> x 10^n (ECMAScript's k digits and exponent n)
    n = len(int_part) + (int(exp) if exp else 0) - lead
    k = len(digits)
    if k <= n <= 21:
        out = digits + "0" * (n - k)
    elif 0 < n <= 21:
        out = digits[:n] + "." + digits[n:]
    elif -6 < n <= 0:
        out = "0." + "0" * (-n) + digits
    else:
        e = n - 1
        es = f"e{'+' if e >= 0 else '-'}{abs(e)}"
        out = digits + es if k == 1 else digits[0] + "." + digits[1:] + es
    return sign + out
