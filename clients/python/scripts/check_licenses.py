"""Checks the licences of the package's runtime dependency closure (ADR 0006 allowlist, ADR 0013).

Run in the project environment: ``uv run python scripts/check_licenses.py``.
Allowed: MIT, BSD, Apache-2.0, ISC (and expressions of them). The
exceptions are listed with their reason; anything else fails.
"""

from __future__ import annotations

import re
import sys
from importlib import metadata

from packaging.requirements import Requirement

ROOT = "xtandard-image"
ALLOWED = re.compile(r"^(MIT|MIT-0|BSD|BSD-[23]-Clause|Apache-2\.0|Apache Software License|ISC)$", re.IGNORECASE)
# Packages whose licence is outside the allowlist, with the reason they are accepted.
EXCEPTIONS = {
    # Mozilla's CA bundle (data), unmodified, pulled in by httpx; MPL-2.0 is
    # file-level copyleft and binds only modifications of its files.
    "certifi": r"MPL-2\.0|MPL 2\.0",
    # Python 3.10 only (Self, Unpack, Required); PSF-2.0 is permissive.
    "typing-extensions": r"PSF-2\.0",
}


def licence(dist: metadata.Distribution) -> str:
    meta = dist.metadata
    expr = meta.get_all("License-Expression")
    if expr:
        return str(expr[0])
    classifiers = [c.split(" :: ")[-1] for c in meta.get_all("Classifier") or [] if c.startswith("License ::")]
    if classifiers:
        return " OR ".join(classifiers)
    text = meta.get_all("License") or ["UNKNOWN"]
    return str(text[0]).splitlines()[0]


def allowed(expr: str) -> bool:
    parts = re.split(r"\s+(?:OR|AND)\s+|[()]", expr)
    terms = [p.strip().removesuffix(" License") for p in parts if p.strip()]
    return bool(terms) and all(ALLOWED.match(t) for t in terms)


def closure() -> list[metadata.Distribution]:
    seen: dict[str, metadata.Distribution] = {}
    todo = [ROOT]
    while todo:
        name = todo.pop()
        key = name.lower().replace("_", "-")
        if key in seen:
            continue
        dist = metadata.distribution(name)
        seen[key] = dist
        for raw in dist.requires or []:
            req = Requirement(raw)
            if req.marker is not None and not req.marker.evaluate({"extra": ""}):
                continue
            todo.append(req.name)
    return [d for k, d in sorted(seen.items()) if k != ROOT]


def main() -> int:
    bad = 0
    for dist in closure():
        name = dist.metadata["Name"].lower().replace("_", "-")
        lic = licence(dist)
        if allowed(lic):
            status = "ok"
        elif name in EXCEPTIONS and re.search(EXCEPTIONS[name], lic):
            status = "exception"
        else:
            status = "NOT ALLOWED"
            bad += 1
        print(f"{status:12} {name} {dist.version}: {lic}")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
