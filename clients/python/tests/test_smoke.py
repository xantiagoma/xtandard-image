"""Smoke test against a running server, off unless XI_SMOKE_URL is set.

The playground demo (compose.playground.yml, unsigned URLs allowed) works::

    XI_SMOKE_URL=http://127.0.0.1:8080 uv run pytest tests/test_smoke.py

It needs the demo's local fixtures (xi://local/…) and, for the /video
cases, a full or gpl image with XI_VIDEO_TRANSFORM_ENABLED.
"""

from __future__ import annotations

import os

import pytest

from xtandard_image import AsyncClient, ByteRange, Client, NotFoundError, QueryURLBuilder, xi_source

BASE = os.environ.get("XI_SMOKE_URL", "")
pytestmark = pytest.mark.skipif(not BASE, reason="set XI_SMOKE_URL to run against a server")

IMAGE = xi_source("local", "jpeg/baseline.jpg")
VIDEO = xi_source("local", "video/bbb-h264-aac.mp4")


def test_health_and_image() -> None:
    with Client(BASE, source_encoding="plain") as xi:
        xi.healthz()
        assert xi.readyz().ready
        res = xi.image(IMAGE, {"rs": ("fit", 64, 64)}, extension="webp")
        assert res.headers["content-type"] == "image/webp"
        info = xi.info(IMAGE, {"size": True, "format": True, "dimensions": True})
        assert info.get("format") == "jpeg"
        with pytest.raises(NotFoundError):
            xi.image(xi_source("local", "jpeg/missing.jpg"))


def test_query_dialect() -> None:
    url = QueryURLBuilder(base_url=BASE).url(IMAGE, params={"w": 32, "f": "png"})
    with Client(BASE) as xi:
        assert xi.media(url).headers["content-type"] == "image/png"


def test_video_range_and_stream() -> None:
    with Client(BASE) as xi:
        res = xi.video(VIDEO, {"vd": 1, "w": 160}, extension="webm", range=ByteRange(0, 99))
        assert res.status_code in (200, 206)
        head = xi.video(VIDEO, {"vd": 1, "w": 160}, extension="webm", method="HEAD")
        assert head.headers["content-type"].startswith("video/webm")
        m = xi.stream(VIDEO, protocol="hls", renditions=[240])
        assert m.body.startswith("#EXTM3U")
        child = next(line for line in m.body.splitlines() if line and not line.startswith("#"))
        assert xi.media(child).status_code == 200


@pytest.mark.anyio
async def test_async() -> None:
    async with AsyncClient(BASE) as xi:
        await xi.healthz()
        info = await xi.info(IMAGE, {"size": True})
        assert info.get("size", 0) > 0
