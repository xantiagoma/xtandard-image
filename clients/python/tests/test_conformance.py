"""clients/spec/conformance.json: the cases every official client runs (ADR 0013).

Positive cases must produce expect.value byte for byte; error cases must
raise URLBuildError with expect.error as its code. The fixture's options are
pipelines of [name, args] pairs; they are built twice here, as option
mappings (url()) and through the fluent chains (option()), and both must
agree.
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Any, cast

import pytest

from xtandard_image import (
    ByteRange,
    ByteSuffix,
    EncryptionKey,
    ImgproxyURLBuilder,
    QueryURLBuilder,
    SigningKey,
    URLBuildError,
    encrypt_pdf_password,
    encrypt_source,
    escape_plain_source,
    escape_query_value,
    range_header,
    sign,
    sign_xi_url,
    source_url,
    xi_source,
)

from .conftest import load_json

if TYPE_CHECKING:
    from collections.abc import Callable

FIXTURE: dict[str, Any] = load_json("clients/spec/conformance.json")
KEYS: dict[str, dict[str, str]] = FIXTURE["keys"]
CASES: list[dict[str, Any]] = FIXTURE["cases"]


def _signing(cfg: dict[str, Any]) -> SigningKey | None:
    s = cfg.get("signing")
    if s is None:
        return None
    k = KEYS["signing"] if s is True else s
    if "signature_size" in cfg:
        return SigningKey(key=k["key"], salt=k["salt"], size=cfg["signature_size"])
    return SigningKey(key=k["key"], salt=k["salt"])


def _encryption(cfg: dict[str, Any]) -> EncryptionKey | None:
    e = cfg.get("encryption")
    if e is None:
        return None
    k = KEYS["encryption"] if e is True else e
    return EncryptionKey(key=k["key"], iv=k.get("iv"))


def _imgproxy(cfg: dict[str, Any]) -> ImgproxyURLBuilder:
    kw: dict[str, Any] = {
        k: cfg[k]
        for k in ("base_url", "unsigned_signature", "source_encoding", "split_source", "separator", "option_names")
        if k in cfg
    }
    return ImgproxyURLBuilder(signing=_signing(cfg), encryption=_encryption(cfg), **kw)


def _query(cfg: dict[str, Any]) -> QueryURLBuilder:
    kw: dict[str, Any] = {k: cfg[k] for k in ("base_url", "mount", "separator", "option_names") if k in cfg}
    return QueryURLBuilder(signing=_signing(cfg), encryption=_encryption(cfg), **kw)


def _mappings(inp: dict[str, Any]) -> list[dict[str, Any]] | None:
    pipelines = inp.get("options")
    if pipelines is None:
        return None
    return [{name: tuple(args) for name, args in pl} for pl in pipelines]


def _one(inp: dict[str, Any]) -> Any:
    m = _mappings(inp)
    if m is None:
        return None
    return m[0] if len(m) == 1 else m


def _stream_kw(inp: dict[str, Any]) -> dict[str, Any]:
    return {k: inp[k] for k in ("protocol", "renditions", "segment_duration", "segment_format") if k in inp}


def _range(r: Any) -> str | ByteRange | ByteSuffix:
    if isinstance(r, str):
        return r
    d = cast("dict[str, int]", r)
    if "suffix" in d:
        return ByteSuffix(d["suffix"])
    return ByteRange(d["start"], d.get("end"))


def run_mapping(case: dict[str, Any]) -> str:
    """The case through the object form (option mappings)."""
    cfg: dict[str, Any] = case.get("config", {})
    inp: dict[str, Any] = case["input"]
    op: str = case["op"]
    common = {k: inp[k] for k in ("source_encoding",) if k in inp}
    ops: dict[str, Callable[[], str]] = {
        "imgproxy.url": lambda: _imgproxy(cfg).url(
            inp["source"],
            _mappings(inp),  # type: ignore[arg-type]
            extension=inp.get("extension"),
            pdf_password=inp.get("pdf_password"),
            **common,
        ),
        "imgproxy.info_url": lambda: _imgproxy(cfg).info_url(
            inp["source"], _one(inp), pdf_password=inp.get("pdf_password"), **common
        ),
        "imgproxy.video_url": lambda: _video_url(cfg, inp),
        "imgproxy.stream_url": lambda: _imgproxy(cfg).stream_url(
            inp["source"], options=_one(inp), **_stream_kw(inp), **common
        ),
        "query.url": lambda: _query(cfg).url(
            inp["source"],
            params=[tuple(p) for p in inp.get("params", [])],
            options=_mappings(inp),  # type: ignore[arg-type]
            extension=inp.get("extension"),
            pdf_password=inp.get("pdf_password"),
        ),
        "query.video_url": lambda: _query(cfg).video_url(inp["source"], _mappings(inp), extension=inp.get("extension")),  # type: ignore[arg-type]
        "query.stream_url": lambda: _query(cfg).stream_url(inp["source"], options=_one(inp), **_stream_kw(inp)),
        "query.imaginary_url": lambda: _query(cfg).imaginary_url(
            inp["endpoint"], inp["source"], [tuple(p) for p in inp.get("params", [])]
        ),
        "sign": lambda: sign(inp["message"], _required(_signing(cfg))),
        "sign_xi_url": lambda: sign_xi_url(inp["url"], _required(_signing(cfg)), mount=inp.get("mount", "")),
        "encrypt_source": lambda: encrypt_source(inp["source"], _required(_encryption(cfg))),
        "encrypt_pdf_password": lambda: encrypt_pdf_password(inp["password"], _required(_encryption(cfg))),
        "xi_source": lambda: xi_source(inp["name"], inp["key"]),
        "source_url": lambda: source_url(inp["scheme"], inp["authority"], inp["key"]),
        "range_header": lambda: range_header(_range(inp["range"])),
        "escape_plain_source": lambda: escape_plain_source(inp["value"]),
        "escape_query_value": lambda: escape_query_value(inp["value"]),
    }
    return ops[op]()


def _video_url(cfg: dict[str, Any], inp: dict[str, Any]) -> str:
    if "pdf_password" in inp:
        pytest.skip("not expressible: video_url() has no pdf_password parameter (the type system rejects it)")
    return _imgproxy(cfg).video_url(
        inp["source"], _one(inp), extension=inp.get("extension"), source_encoding=inp.get("source_encoding")
    )


def _required(v: Any) -> Any:
    assert v is not None
    return v


def run_fluent(case: dict[str, Any]) -> str | None:
    """The case through the fluent chains, for the ops that have one."""
    cfg: dict[str, Any] = case.get("config", {})
    inp: dict[str, Any] = case["input"]
    pipelines: list[list[list[Any]]] = inp.get("options", [])
    op: str = case["op"]
    chain: Any
    if op == "imgproxy.url":
        chain = _imgproxy(cfg).image(inp["source"])
    elif op == "imgproxy.info_url":
        chain = _imgproxy(cfg).info(inp["source"])
    elif op == "imgproxy.video_url" and "pdf_password" not in inp:
        chain = _imgproxy(cfg).video(inp["source"])
    elif op == "query.url":
        chain = _query(cfg).image(inp["source"])
        for k, v in inp.get("params", []):
            chain.param(k, v)
    elif op == "query.video_url":
        chain = _query(cfg).video(inp["source"])
    else:
        return None
    for i, pl in enumerate(pipelines):
        if i > 0:
            if not hasattr(chain, "pipeline"):
                return None  # /info and /video chains cannot chain; the mapping form covers the error
            chain.pipeline()
        for name, args in pl:
            chain.option(name, *args)
    if "source_encoding" in inp:
        chain.encoding(inp["source_encoding"])
    if inp.get("extension") is not None:
        chain.extension(inp["extension"])
    if "pdf_password" in inp:
        chain.with_pdf_password(inp["pdf_password"])
    return cast("str", chain.build())


def test_fixture_covers_every_op() -> None:
    ops = {c["op"] for c in CASES}
    assert len(ops) >= 17
    assert len(CASES) >= 100


@pytest.mark.parametrize("case", CASES, ids=[f"{c['op']}:{c['name']}" for c in CASES])
def test_case(case: dict[str, Any]) -> None:
    expect: dict[str, str] = case["expect"]
    if "error" in expect:
        with pytest.raises(URLBuildError) as exc:
            run_mapping(case)
        assert exc.value.code == expect["error"]
        if run_fluent_safe(case):
            with pytest.raises(URLBuildError) as exc2:
                run_fluent(case)
            assert exc2.value.code == expect["error"]
        return
    assert run_mapping(case) == expect["value"]
    fluent = run_fluent(case)
    if fluent is not None:
        assert fluent == expect["value"]


def run_fluent_safe(case: dict[str, Any]) -> bool:
    """Whether the fluent form reaches the same error (it cannot express chained /info or /video URLs)."""
    try:
        return run_fluent(case) is not None
    except URLBuildError:
        return True
