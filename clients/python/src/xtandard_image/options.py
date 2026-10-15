"""Option catalogues at run time and their serialisation into option segments."""

from __future__ import annotations

import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from typing import Literal, cast

from ._generated.methods import Arg
from ._generated.options import INFO_NAMES, INFO_SHORT, PROCESSING_NAMES, PROCESSING_SHORT, VIDEO_NAMES, VIDEO_SHORT
from ._text import format_number
from .errors import URLBuildError

OptionNameStyle = Literal["short", "canonical", "given"]
"""How option names are written: the shortest alias (default), the canonical name, or as given."""

OptionEntry = tuple[str, tuple[Arg, ...]]
"""One option occurrence: a name (canonical or alias) and its arguments."""


@dataclass(frozen=True)
class Catalogue:
    """One option catalogue: every name and alias to its canonical name, and canonical to short names."""

    kind: str
    names: Mapping[str, str]
    short: Mapping[str, str]


PROCESSING = Catalogue("imgproxy", PROCESSING_NAMES, PROCESSING_SHORT)
INFO = Catalogue("info", INFO_NAMES, INFO_SHORT)
VIDEO = Catalogue("video", VIDEO_NAMES, VIDEO_SHORT)

# Characters an option argument cannot contain besides the separator.
_BAD_ARG = re.compile(r"[/?#\s\x00-\x1f\x7f﻿]")


def entries_of(options: Mapping[str, object]) -> list[OptionEntry]:
    """Converts an options mapping into entries, in key order; None values are skipped."""
    out: list[OptionEntry] = []
    for name, value in options.items():
        if value is None:
            continue
        if isinstance(value, tuple | list):
            items = cast("Sequence[object]", value)
            out.append((name, tuple(_as_arg(name, a) for a in items)))
        else:
            out.append((name, (_as_arg(name, value),)))
    return out


def _as_arg(name: str, a: object) -> Arg:
    if a is None or isinstance(a, str | int | float | bool):
        return a
    raise URLBuildError("invalid_argument", f"option {name}: unsupported argument type {type(a).__name__}")


def pipelines_of(options: Mapping[str, object] | Sequence[Mapping[str, object]] | None) -> list[list[OptionEntry]]:
    """One entry list per pipeline: a mapping is one pipeline, a sequence of mappings chains them."""
    if options is None:
        return [[]]
    if isinstance(options, Mapping):
        return [entries_of(options)]
    pipelines = [entries_of(o) for o in options]
    return pipelines or [[]]


def format_arg(a: Arg, sep: str, where: str) -> str:
    """Formats one argument.

    Strings are checked, never escaped: the server does not unescape option
    arguments.
    """
    if a is None:
        return ""
    if isinstance(a, bool):
        return "1" if a else "0"
    if isinstance(a, int | float):
        return format_number(a, where)
    if sep in a or _BAD_ARG.search(a):
        raise URLBuildError(
            "invalid_argument", f"{where}: argument {a!r} contains a character that cannot appear in an option"
        )
    return a


def written_name(cat: Catalogue, name: str, style: OptionNameStyle) -> str:
    """The name an option is written with, or URLBuildError for an unknown name."""
    canonical = cat.names.get(name)
    if canonical is None:
        raise URLBuildError("unknown_option", f"unknown {cat.kind} option {name!r}")
    if style == "given":
        return name
    if style == "canonical":
        return canonical
    return cat.short.get(canonical, canonical)


def format_entry(cat: Catalogue, entry: OptionEntry, sep: str, style: OptionNameStyle) -> str:
    """Formats one option segment (``rs:fill:300:400``)."""
    name, args = entry
    written = written_name(cat, name, style)
    if not args:
        raise URLBuildError("invalid_argument", f"option {name}: at least one argument is required")
    return sep.join([written, *(format_arg(a, sep, f"option {name} argument {i + 1}") for i, a in enumerate(args))])
