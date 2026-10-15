"""Source URL and PDF password encryption, byte-compatible with the server (internal/security/crypt).

``token = base64url_unpadded(IV[16] ‖ AES-CBC(key, IV, PKCS#7(utf8(plaintext))))``.
Encrypt on a server: the key decrypts every token.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field

from cryptography.hazmat.primitives import padding
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

from ._generated.constants import ENCRYPTION_KEY_SIZES, IV_SIZE, MAX_PDF_PASSWORD_BYTES
from ._text import KeyMaterial, b64url, check_text, key_bytes, utf8
from .errors import URLBuildError

__all__ = ["MAX_PDF_PASSWORD_BYTES", "EncryptionKey", "encrypt_pdf_password", "encrypt_source"]


@dataclass(frozen=True)
class EncryptionKey:
    """An encryption key: the first of XI_ENCRYPTION_KEYS (imgproxy IMGPROXY_SOURCE_URL_ENCRYPTION_KEY).

    Attributes:
        key: AES key, hex or raw bytes: 16, 24 or 32 bytes.
        iv: A fixed 16-byte IV (hex or raw bytes). Leave it None for a fresh
            random IV per token, as the server's own encoder does. A fixed IV
            makes URLs deterministic and reveals when two URLs share a
            source; use it only for tests or cache keys.
    """

    key: KeyMaterial = field(repr=False)
    iv: KeyMaterial | None = field(default=None, repr=False)

    def __post_init__(self) -> None:
        k = key_bytes(self.key, "encryption key")
        if len(k) not in ENCRYPTION_KEY_SIZES:
            raise URLBuildError("invalid_key", f"encryption key is {len(k)} bytes, want 16, 24 or 32")
        if self.iv is not None and len(key_bytes(self.iv, "iv")) != IV_SIZE:
            raise URLBuildError("invalid_key", f"iv is not {IV_SIZE} bytes")


def _seal(plaintext: str, k: EncryptionKey) -> str:
    iv = os.urandom(IV_SIZE) if k.iv is None else key_bytes(k.iv, "iv")
    padder = padding.PKCS7(128).padder()
    data = padder.update(utf8(plaintext)) + padder.finalize()
    enc = Cipher(algorithms.AES(key_bytes(k.key, "encryption key")), modes.CBC(iv)).encryptor()
    return b64url(iv + enc.update(data) + enc.finalize())


def encrypt_source(source: str, key: EncryptionKey) -> str:
    """Encrypts a source URL into an ``enc/`` token.

    Raises:
        URLBuildError: The source is empty or has control characters (``invalid_source``).
    """
    return _seal(check_text(source, "invalid_source", "source"), key)


def encrypt_pdf_password(password: str, key: EncryptionKey) -> str:
    """Encrypts the password of a password-protected PDF into a ``pdf_password`` (``pdp``) token.

    The token is the ``enc/`` format over the password, with the same key.
    The server accepts the option on signed URLs only.

    Raises:
        URLBuildError: The password is empty, has control characters or is
            longer than 1024 UTF-8 bytes (``invalid_password``).
    """
    check_text(password, "invalid_password", "password")
    if len(utf8(password)) > MAX_PDF_PASSWORD_BYTES:
        raise URLBuildError("invalid_password", f"password is longer than {MAX_PDF_PASSWORD_BYTES} bytes")
    return _seal(password, key)
