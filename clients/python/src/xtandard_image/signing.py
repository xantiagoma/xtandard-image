"""URL signing, byte-compatible with the server (internal/security/sign).

* imgproxy: ``base64url(HMAC-SHA256(key, salt ‖ path)[:size])``, the signature
  segment of ``/{signature}/{options}/{source}``; ``/video`` URLs sign
  ``"/video" + path``.
* xi_sig: the same construction over ``path ‖ "?" ‖ query-without-xi_sig``,
  appended as the last query parameter (docs/URL-DIALECTS.md §2).
"""

from __future__ import annotations

import hashlib
import hmac
import re
from dataclasses import dataclass, field

from ._generated.constants import DEFAULT_SIGNATURE_SIZE, MAX_SIGNATURE_SIZE, MIN_SIGNATURE_SIZE, XI_SIG_PARAM
from ._text import KeyMaterial, b64url, key_bytes, utf8
from .errors import URLBuildError

__all__ = ["XI_SIG_PARAM", "SigningKey", "sign", "sign_imgproxy_path", "sign_xi_query", "sign_xi_url", "xi_message"]


@dataclass(frozen=True)
class SigningKey:
    """A signing key: XI_SIGNING_KEYS / XI_SIGNING_SALTS (imgproxy IMGPROXY_KEY / IMGPROXY_SALT).

    Attributes:
        key: HMAC key, hex (as in the server config) or raw bytes.
        salt: Salt prepended to the message, hex or raw bytes.
        size: Signature size in bytes before encoding, 1 to 32 (XI_SIGNING_SIZE). Default 32.
    """

    key: KeyMaterial = field(repr=False)
    salt: KeyMaterial = field(repr=False)
    size: int = DEFAULT_SIGNATURE_SIZE

    def __post_init__(self) -> None:
        if isinstance(self.size, bool) or not MIN_SIGNATURE_SIZE <= self.size <= MAX_SIGNATURE_SIZE:
            raise URLBuildError(
                "invalid_key", f"signature size {self.size} out of range [{MIN_SIGNATURE_SIZE}, {MAX_SIGNATURE_SIZE}]"
            )
        key_bytes(self.key, "signing key")
        key_bytes(self.salt, "signing salt")


def sign(message: str, key: SigningKey) -> str:
    """Signs a message with the salted HMAC-SHA256 construction of the imgproxy and xi_sig schemes.

    Like the server, a message without a leading ``/`` is signed as if it had one.
    """
    m = message if message.startswith("/") else "/" + message
    mac = hmac.new(key_bytes(key.key, "signing key"), key_bytes(key.salt, "signing salt") + utf8(m), hashlib.sha256)
    return b64url(mac.digest()[: key.size])


def sign_imgproxy_path(path: str, key: SigningKey) -> str:
    """Returns ``"/" + signature + path`` for an imgproxy path (everything after the signature segment)."""
    p = path if path.startswith("/") else "/" + path
    return f"/{sign(p, key)}{p}"


def xi_message(path: str, raw_query: str) -> tuple[str | None, str]:
    """Splits a raw query into its xi_sig value and the message xi_sig signs.

    The message is the path, then ``?`` and the remaining non-empty
    parameters in their original order and encoding.

    Raises:
        URLBuildError: The query has more than one xi_sig (the server answers 403).
    """
    sig: str | None = None
    kept: list[str] = []
    for part in raw_query.split("&"):
        if part == "":
            continue
        k, eq, v = part.partition("=")
        if k == XI_SIG_PARAM:
            if sig is not None:
                raise URLBuildError("invalid_request", f"query has more than one {XI_SIG_PARAM} parameter")
            sig = v if eq else ""
            continue
        kept.append(part)
    return sig, (f"{path}?{'&'.join(kept)}" if kept else path)


def sign_xi_query(path: str, raw_query: str, key: SigningKey) -> str:
    """Returns raw_query without any xi_sig and with a fresh xi_sig for path appended last.

    ``path`` is the escaped path the dialect sees, after its mount prefix.
    """
    _, msg = xi_message(path, raw_query.removeprefix("?"))
    sig = sign(msg, key)
    q = msg[len(path) :]
    return f"{XI_SIG_PARAM}={sig}" if q == "" else f"{q[1:]}&{XI_SIG_PARAM}={sig}"


_ABSOLUTE = re.compile(r"^[a-zA-Z][a-zA-Z0-9+.-]*://")


def sign_xi_url(url: str, key: SigningKey, *, mount: str = "") -> str:
    """Signs a URL (absolute, or a path with an optional query) with xi_sig.

    ``mount`` is the dialect's mount prefix, which the signed path excludes
    (``"/q"`` for the query dialect). A fragment is dropped.

    Raises:
        URLBuildError: The path is not under the mount, or the query has two xi_sig.
    """
    no_hash = url.split("#", 1)[0]
    head, _, query = no_hash.partition("?")
    path_start = head.find("/", head.index("//") + 2) if _ABSOLUTE.match(head) else 0
    origin = head if path_start < 0 else head[:path_start]
    path = "/" if path_start < 0 else head[path_start:]
    m = mount.rstrip("/")
    if m:
        if path != m and not path.startswith(m + "/"):
            raise URLBuildError("invalid_request", f"path {path} is not under the mount {m}")
        path = path[len(m) :] or "/"
    return f"{origin}{m}{path}?{sign_xi_query(path, query, key)}"
