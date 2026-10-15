"""testdata/security/vectors.json: the signing, enc/ and pdf_password vectors shared with Go and TypeScript."""

from __future__ import annotations

import base64
from typing import Any

import pytest
from cryptography.hazmat.primitives import padding
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

from xtandard_image import (
    EncryptionKey,
    SigningKey,
    URLBuildError,
    encrypt_pdf_password,
    encrypt_source,
    sign,
    sign_imgproxy_path,
    sign_xi_query,
    sign_xi_url,
    xi_message,
)

from .conftest import load_json

V: dict[str, Any] = load_json("testdata/security/vectors.json")


def _decrypt(token: str, key: str) -> str:
    raw = base64.urlsafe_b64decode(token + "=" * (-len(token) % 4))
    d = Cipher(algorithms.AES(bytes.fromhex(key)), modes.CBC(raw[:16])).decryptor()
    padded = d.update(raw[16:]) + d.finalize()
    u = padding.PKCS7(128).unpadder()
    return (u.update(padded) + u.finalize()).decode()


@pytest.mark.parametrize("v", V["imgproxy"], ids=lambda v: v["name"])
def test_imgproxy(v: dict[str, Any]) -> None:
    k = SigningKey(key=v["key"], salt=v["salt"], size=v["size"])
    assert sign(v["path"], k) == v["signature"]
    p = v["path"] if v["path"].startswith("/") else "/" + v["path"]
    assert sign_imgproxy_path(v["path"], k) == f"/{v['signature']}{p}"
    raw = SigningKey(key=bytes.fromhex(v["key"]), salt=bytes.fromhex(v["salt"]), size=v["size"])
    assert sign(v["path"], raw) == v["signature"]


@pytest.mark.parametrize("v", V["xi_sig"], ids=lambda v: v["name"])
def test_xi_sig(v: dict[str, Any]) -> None:
    k = SigningKey(key=v["key"], salt=v["salt"], size=v["size"])
    assert sign_xi_query(v["path"], v["query"], k) == v["signed_query"]
    assert sign_xi_query(v["path"], v["signed_query"], k) == v["signed_query"]
    assert sign_xi_url(f"https://img.example.com{v['path']}?{v['query']}", k) == (
        f"https://img.example.com{v['path']}?{v['signed_query']}"
    )


@pytest.mark.parametrize("v", V["encryption"], ids=lambda v: v["name"])
def test_encryption(v: dict[str, Any]) -> None:
    token = encrypt_source(v["plaintext"], EncryptionKey(key=v["key"], iv=v["iv"]))
    assert token == v["token"]
    assert _decrypt(token, v["key"]) == v["plaintext"]


@pytest.mark.parametrize("v", V["pdf_password"], ids=lambda v: v["name"])
def test_pdf_password(v: dict[str, Any]) -> None:
    assert encrypt_pdf_password(v["password"], EncryptionKey(key=v["key"], iv=v["iv"])) == v["token"]


def test_random_iv() -> None:
    k = EncryptionKey(key=V["pdf_password"][0]["key"])
    a, b = encrypt_pdf_password("s3cret", k), encrypt_pdf_password("s3cret", k)
    assert a != b
    assert _decrypt(a, V["pdf_password"][0]["key"]) == "s3cret"


def test_xi_message() -> None:
    assert xi_message("/", "url=x&&xi_sig=s&w=1") == ("s", "/?url=x&w=1")
    assert xi_message("/", "") == (None, "/")
    with pytest.raises(URLBuildError) as e:
        xi_message("/", "xi_sig=a&xi_sig=b")
    assert e.value.code == "invalid_request"


def test_signing_key_repr_hides_secrets() -> None:
    k = SigningKey(key="00" * 32, salt="11" * 32)
    assert "00" not in repr(k)
    assert "11" not in repr(k)
