"""testdata/urls/imgproxy-builder.json: 400+ cases derived from the server's golden tables.

The URLs were written by the TypeScript client and parsed back by the Go
dialect tests (TestBuilderFixture); every client must write the same ones:
``url`` with option names as given, ``url_short`` with the default short
names, and ``query_url`` (query dialect) where the case has one.
"""

from __future__ import annotations

from typing import Any

import pytest

from xtandard_image import EncryptionKey, ImgproxyURLBuilder, QueryURLBuilder, SigningKey

from .conftest import load_json

FIXTURE: dict[str, Any] = load_json("testdata/urls/imgproxy-builder.json")
CASES: list[dict[str, Any]] = FIXTURE["cases"]


def _signing(c: dict[str, Any]) -> SigningKey | None:
    size = c.get("signature_size")
    if not size:
        return None
    return SigningKey(key=FIXTURE["signing"]["key"], salt=FIXTURE["signing"]["salt"], size=size)


def _fluent(c: dict[str, Any], *, given: bool) -> str:
    b = ImgproxyURLBuilder(
        unsigned_signature="unsafe",
        encryption=EncryptionKey(key=FIXTURE["encryption"]["key"], iv=FIXTURE["encryption"]["iv"]),
        signing=_signing(c),
        separator=c.get("separator", ":"),
        option_names="given" if given else "short",
    )
    endpoint = c.get("endpoint")
    chain: Any = (
        b.info(c["source"])
        if endpoint == "info"
        else b.video(c["source"])
        if endpoint == "video"
        else b.image(c["source"])
    )
    chain.encoding(c["encoding"])
    for i, pl in enumerate(c["pipelines"]):
        if i > 0:
            chain.pipeline()
        for entry in pl:
            for name, args in entry.items():
                chain.option(name, *args)
    if c.get("extension"):
        chain.extension(c["extension"])
    return str(chain.build())


def _query(c: dict[str, Any]) -> str:
    q = QueryURLBuilder(signing=_signing(c))
    chain: Any = q.video(c["source"]) if c.get("endpoint") == "video" else q.image(c["source"])
    for i, pl in enumerate(c["pipelines"]):
        if i > 0:
            chain.pipeline()
        for entry in pl:
            for name, args in entry.items():
                chain.option(name, *args)
    if c.get("extension"):
        chain.extension(c["extension"])
    return str(chain.build())


def test_covers_the_tables() -> None:
    assert len(CASES) >= 400


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
def test_case(case: dict[str, Any]) -> None:
    assert _fluent(case, given=True) == case["url"]
    assert _fluent(case, given=False) == case["url_short"]
    if case.get("query"):
        assert _query(case) == case["query_url"]
