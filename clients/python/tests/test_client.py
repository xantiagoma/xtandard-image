"""The HTTP clients against a stand-in server (httpx.MockTransport)."""

from __future__ import annotations

import json
import re

import httpx
import pytest

from xtandard_image import (
    AsyncClient,
    BadRequestError,
    ByteRange,
    ByteSuffix,
    Client,
    ForbiddenError,
    GatewayError,
    HTTPError,
    ImgproxyURLBuilder,
    NotFoundError,
    ServiceUnavailableError,
    SigningKey,
    TooManyRequestsError,
    TransportError,
    URLBuildError,
    range_header,
)

KEY = SigningKey(key="736563726574", salt="68656C6C6F")
BASE = "http://xi.test"
BODY = bytes(i % 256 for i in range(1000))
VIDEO_URL = ImgproxyURLBuilder(base_url=BASE, signing=KEY).video_url("s3://m/clip.mov", {"vd": 2}, extension="webm")


def handler(seen: list[httpx.Request]) -> httpx.MockTransport:
    def h(req: httpx.Request) -> httpx.Response:
        seen.append(req)
        path = req.url.path
        if path == "/healthz":
            return httpx.Response(200, text="ok\n")
        if path == "/readyz":
            return httpx.Response(503, text="draining\n")
        if path.startswith("/info/"):
            return httpx.Response(200, json={"width": 640, "height": 480, "format": "jpeg"})
        if path.startswith("/video/") and path.endswith(".m3u8"):
            return httpx.Response(
                200, text="#EXTM3U\n/_stream/T/v0.m3u8\n", headers={"Content-Type": "application/vnd.apple.mpegurl"}
            )
        if path.startswith(("/video/", "/_stream/")):
            if path.startswith("/video/") and str(req.url) != VIDEO_URL:
                return httpx.Response(403, json={"error": {"kind": "unauthorized", "message": "invalid signature"}})
            m = re.fullmatch(r"bytes=(\d+)-(\d*)", req.headers.get("range", ""))
            if m:
                start = int(m[1])
                end = int(m[2]) if m[2] else len(BODY) - 1
                return httpx.Response(
                    206,
                    content=b"" if req.method == "HEAD" else BODY[start : end + 1],
                    headers={"Content-Type": "video/webm", "Content-Range": f"bytes {start}-{end}/{len(BODY)}"},
                )
            return httpx.Response(
                200, content=b"" if req.method == "HEAD" else BODY, headers={"Content-Type": "video/webm"}
            )
        if path == "/_admin/play/api/meta":
            return httpx.Response(200, json={"version": "test"})
        if path == "/proxy-error":
            return httpx.Response(502, text="<html>bad gateway</html>")
        if path == "/busy":
            return httpx.Response(
                429, json={"error": {"kind": "overloaded", "message": "too many requests", "request_id": "r1"}}
            )
        if path == "/down":
            raise httpx.ConnectError("refused", request=req)
        return httpx.Response(404, json={"error": {"kind": "not_found", "message": "source image not found"}})

    return httpx.MockTransport(h)


@pytest.fixture
def seen() -> list[httpx.Request]:
    return []


@pytest.fixture
def client(seen: list[httpx.Request]) -> Client:
    return Client(BASE, signing=KEY, http_client=httpx.Client(transport=handler(seen)), playground_path="/_admin/play/")


def test_health(client: Client) -> None:
    client.healthz()
    r = client.readyz()
    assert (r.ready, r.state) == (False, "draining")


def test_info(client: Client, seen: list[httpx.Request]) -> None:
    info = client.info("s3://b/cat.jpg", {"size": True, "format": True})
    assert info.get("width") == 640
    assert seen[-1].headers["accept"] == "application/json"
    assert seen[-1].url.path.startswith("/info/")
    assert client.info_at(str(seen[-1].url.path)).get("format") == "jpeg"


def test_video_whole_range_head(client: Client, seen: list[httpx.Request]) -> None:
    res = client.video("s3://m/clip.mov", {"vd": 2}, extension="webm")
    assert res.content == BODY
    res = client.video("s3://m/clip.mov", {"vd": 2}, extension="webm", range=ByteRange(10, 19))
    assert (res.status_code, res.headers["content-range"]) == (206, "bytes 10-19/1000")
    assert res.content == BODY[10:20]
    assert seen[-1].headers["range"] == "bytes=10-19"
    res = client.video("s3://m/clip.mov", {"vd": 2}, extension="webm", method="HEAD")
    assert seen[-1].method == "HEAD"
    assert res.headers["content-type"] == "video/webm"


def test_video_streamed_body(client: Client) -> None:
    res = client.video("s3://m/clip.mov", {"vd": 2}, extension="webm", stream=True)
    try:
        assert b"".join(res.iter_bytes()) == BODY
    finally:
        res.close()


def test_bad_signature_is_forbidden(client: Client) -> None:
    with pytest.raises(ForbiddenError) as e:
        client.video("s3://m/clip.mov", {"vd": 3}, extension="webm")
    assert (e.value.status, e.value.kind, e.value.message) == (403, "unauthorized", "invalid signature")


def test_stream_and_ticket_child(client: Client, seen: list[httpx.Request]) -> None:
    m = client.stream("s3://m/clip.mov", protocol="hls", renditions=[360, 720])
    assert "/st:hls:360:720/" in m.url
    assert m.content_type == "application/vnd.apple.mpegurl"
    child = m.body.splitlines()[1]
    res = client.media(child, range=ByteSuffix(4))
    assert seen[-1].url.path == "/_stream/T/v0.m3u8"
    assert seen[-1].headers["range"] == "bytes=-4"
    assert res.status_code == 200  # the stand-in only answers start-end ranges


def test_errors_by_status(client: Client) -> None:
    with pytest.raises(NotFoundError) as nf:
        client.image("s3://b/missing.jpg")
    assert nf.value.kind == "not_found"
    with pytest.raises(GatewayError) as gw:
        client.media("/proxy-error")
    assert (gw.value.kind, gw.value.message, gw.value.body) == ("unknown", "<html>bad gateway</html>", None)
    with pytest.raises(TooManyRequestsError) as busy:
        client.media("/busy")
    assert busy.value.request_id == "r1"
    with pytest.raises(TransportError):
        client.media("/down")
    assert issubclass(ServiceUnavailableError, HTTPError)
    assert issubclass(BadRequestError, HTTPError)


def test_playground_meta(client: Client) -> None:
    assert client.playground_meta() == {"version": "test"}


def test_range_header() -> None:
    assert range_header(ByteRange(0)) == "bytes=0-"
    assert range_header(ByteRange(5, 9)) == "bytes=5-9"
    assert range_header(ByteSuffix(100)) == "bytes=-100"
    assert range_header("bytes=1-2") == "bytes=1-2"
    for bad in (ByteRange(5, 4), ByteRange(-1), ByteSuffix(-1)):
        with pytest.raises(URLBuildError):
            range_header(bad)


def test_owned_client_closes() -> None:
    with Client(BASE) as c:
        assert c.urls.config.base_url == BASE
    assert c._http.is_closed  # pyright: ignore[reportPrivateUsage]


@pytest.mark.anyio
async def test_async_client(seen: list[httpx.Request]) -> None:
    async with AsyncClient(
        BASE, signing=KEY, http_client=httpx.AsyncClient(transport=handler(seen)), playground_path="/_admin/play/"
    ) as xi:
        await xi.healthz()
        assert (await xi.readyz()).state == "draining"
        assert (await xi.info("s3://b/cat.jpg", {"size": True})).get("height") == 480
        res = await xi.video("s3://m/clip.mov", {"vd": 2}, extension="webm", range=ByteRange(0, 9))
        assert res.content == BODY[:10]
        m = await xi.stream("s3://m/clip.mov", protocol="hls")
        assert m.body.startswith("#EXTM3U")
        with pytest.raises(ForbiddenError):
            await xi.video("s3://m/clip.mov", {"vd": 3}, extension="webm")
        with pytest.raises(TransportError):
            await xi.media("/down")
        assert await xi.playground_meta() == {"version": "test"}
        r = await xi.video("s3://m/clip.mov", {"vd": 2}, extension="webm", stream=True)
        try:
            assert b"".join([c async for c in r.aiter_bytes()]) == BODY
        finally:
            await r.aclose()


def test_error_body_is_json_envelope() -> None:
    body = json.dumps({"error": {"kind": "invalid_url", "message": "invalid url"}})
    req = httpx.Request("GET", f"{BASE}/x")
    from xtandard_image.client import to_http_error  # noqa: PLC0415

    err = to_http_error(httpx.Response(400, text=body, request=req))
    assert isinstance(err, BadRequestError)
    assert (err.kind, err.message, err.url) == ("invalid_url", "invalid url", f"{BASE}/x")
