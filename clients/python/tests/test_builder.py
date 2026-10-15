"""Unit tests of the builders beyond the shared fixtures: Python-specific forms and number formatting."""

from __future__ import annotations

import math
from typing import Any, cast

import pytest

from xtandard_image import (
    ImgproxyURLBuilder,
    ProcessingOptions,
    QueryURLBuilder,
    URLBuildError,
    VideoOptions,
    imgproxy,
    query_urls,
)
from xtandard_image._text import format_number

SRC = "https://example.com/a.jpg"


@pytest.mark.parametrize(
    ("value", "want"),
    [
        (0, "0"),
        (-0.0, "0"),
        (300, "300"),
        (300.0, "300"),
        (1.5, "1.5"),
        (-1.5, "-1.5"),
        (0.1, "0.1"),
        (0.000001, "0.000001"),
        (1e-7, "1e-7"),
        (1.5e-9, "1.5e-9"),
        (123456789.125, "123456789.125"),
        (1e20, "100000000000000000000"),
        (1e21, "1e+21"),
        (1.5e22, "1.5e+22"),
        (10**21, "1e+21"),
        (2**53 + 1, "9007199254740993"),
        (0.30000000000000004, "0.30000000000000004"),
    ],
)
def test_format_number_matches_ecmascript(value: float, want: str) -> None:
    assert format_number(value, "x") == want


@pytest.mark.parametrize("value", [math.inf, -math.inf, math.nan])
def test_format_number_rejects_non_finite(value: float) -> None:
    with pytest.raises(URLBuildError) as e:
        format_number(value, "x")
    assert e.value.code == "invalid_argument"


def test_bare_values_and_none() -> None:
    opts: ProcessingOptions = {"rs": ("fill", 300, 400), "q": 80, "g": "sm", "bl": None}
    assert imgproxy().url(SRC, opts) == "/insecure/rs:fill:300:400/q:80/g:sm/aHR0cHM6Ly9leGFtcGxlLmNvbS9hLmpwZw"


def test_lists_are_accepted_at_run_time() -> None:
    loose: Any = {"rs": ["fit", 10, 10]}
    assert imgproxy().url(SRC, loose) == imgproxy().url(SRC, {"rs": ("fit", 10, 10)})


def test_chained_mappings() -> None:
    url = imgproxy(source_encoding="plain").url(SRC, [{"rs": ("fit", 100, 100)}, {"bl": 2}])
    assert url == "/insecure/rs:fit:100:100/-/bl:2/plain/https://example.com/a.jpg"


def test_fluent_methods_use_the_name_called() -> None:
    b = imgproxy(option_names="given", source_encoding="plain")
    assert b.image(SRC).resize("fit", 1, 2).q(90).min_width(10).build() == (
        "/insecure/resize:fit:1:2/q:90/min-width:10/plain/https://example.com/a.jpg"
    )


def test_fluent_str() -> None:
    assert str(imgproxy().image(SRC).w(100)) == imgproxy().url(SRC, {"w": 100})


def test_video_and_stream() -> None:
    b = ImgproxyURLBuilder(source_encoding="plain")
    opts: VideoOptions = {"rs": ("fill", 640, 360), "vs": 1.5, "vd": 5}
    assert b.video_url("s3://m/a.mov", opts, extension="webm") == (
        "/video/insecure/rs:fill:640:360/vs:1.5/vd:5/plain/s3://m/a.mov@webm"
    )
    assert b.video("s3://m/a.mov").start(2).mute(True).extension("mp4").build() == (
        "/video/insecure/vs:2/mu:1/plain/s3://m/a.mov@mp4"
    )
    assert b.stream_url("s3://m/a.mov", protocol="dash", renditions=[360]) == (
        "/video/insecure/st:dash:360/plain/s3://m/a.mov@mpd"
    )


def test_query_fluent_and_mapping_params() -> None:
    q = QueryURLBuilder()
    assert q.url(SRC, params={"w": 300, "enlarge": True}) == "/q/?url=https://example.com/a.jpg&w=300&enlarge=true"
    assert q.image(SRC).param("w", 300).bl(2).build() == "/q/?url=https://example.com/a.jpg&w=300&o=bl:2"
    assert (
        query_urls().imaginary_url("resize", SRC, {"width": 10}) == "/q/resize?url=https://example.com/a.jpg&width=10"
    )


def test_unsupported_argument_type() -> None:
    with pytest.raises(URLBuildError) as e:
        imgproxy().url(SRC, cast("Any", {"w": object()}))
    assert e.value.code == "invalid_argument"


def test_url_build_error_is_a_value_error() -> None:
    with pytest.raises(ValueError, match="unknown"):
        imgproxy().image(SRC).option("nope", 1).build()
