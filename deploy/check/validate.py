"""Offline validators for deploy/ files that have no CLI validator without a cloud account.

  python validate.py openapi <spec-url-or-path> <definition> <instance.yaml> [--pick key]
      Checks an instance against one schema of an OpenAPI 2/3 document (DigitalOcean app spec,
      Azure Container Apps): types, enums, required keys, numeric bounds and, unlike the
      platforms' own loose schemas, unknown keys (a typo fails). Remote $refs are fetched.
  python validate.py botocore <service> <Operation> <params.json>
      Checks AWS CLI input JSON against botocore's service model (no credentials, no calls).
  python validate.py env <compose|fly|knative|aca|render|do|ecs|apprunner> <file> <outdir>
      Writes the XI_* variables of each container in a platform file to <outdir>/<n>.env
      (secrets replaced by a dummy hex key) for `xtandard-image config validate`.

Run by deploy/check/schemas.sh with pinned pyyaml and botocore.
"""

from __future__ import annotations

import json
import sys
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

import yaml

_docs: dict[str, Any] = {}


def load(uri: str) -> Any:
    if uri not in _docs:
        if uri.startswith(("http://", "https://")):
            with urllib.request.urlopen(uri, timeout=60) as r:  # noqa: S310 (fixed hosts)
                text = r.read().decode()
        else:
            text = Path(uri).read_text()
        _docs[uri] = yaml.load(text, Loader=getattr(yaml, "CSafeLoader", yaml.SafeLoader))
    return _docs[uri]


def deref(schema: dict[str, Any], base: str) -> tuple[dict[str, Any], str]:
    seen = 0
    while isinstance(schema, dict) and "$ref" in schema:
        ref = schema["$ref"]
        target, _, frag = ref.partition("#")
        if target:
            base = urllib.parse.urljoin(base, target) if "://" in base else str(Path(base).parent / target)
        node: Any = load(base)
        for part in [p for p in frag.split("/") if p]:
            node = node[part.replace("~1", "/").replace("~0", "~")]
        schema = node
        seen += 1
        if seen > 50:
            raise RuntimeError(f"$ref loop at {ref}")
    return schema, base


def flatten(schema: dict[str, Any], base: str) -> list[tuple[dict[str, Any], str]]:
    """The schema and every allOf member, dereferenced."""
    schema, base = deref(schema, base)
    out = [(schema, base)]
    for sub in schema.get("allOf", []):
        out.extend(flatten(sub, base))
    return out


TYPES = {
    "string": (str,),
    "integer": (int,),
    "number": (int, float),
    "boolean": (bool,),
    "object": (dict,),
    "array": (list,),
}


def check(inst: Any, schema: dict[str, Any], base: str, path: str, errs: list[str]) -> None:
    parts = flatten(schema, base)
    props: dict[str, tuple[dict[str, Any], str]] = {}
    open_object = False
    for s, b in parts:
        for k, v in s.get("properties", {}).items():
            props.setdefault(k, (v, b))
        ap = s.get("additionalProperties")
        if ap not in (None, False):
            open_object = True
        t = s.get("type")
        if t == "string" and isinstance(inst, bool) and str(s.get("example", "")).lower() in ("true", "false"):
            # DigitalOcean's OpenAPI types preserve_path_prefix as a string with example 'true'
            # while its docs and godo use a boolean: accept a boolean for such flags.
            continue
        if t in TYPES and inst is not None:
            ok = isinstance(inst, TYPES[t]) and not (t in ("integer", "number") and isinstance(inst, bool))
            if not ok:
                errs.append(f"{path}: expected {t}, got {type(inst).__name__}")
                return
        if "enum" in s and inst not in s["enum"]:
            errs.append(f"{path}: {inst!r} not in {s['enum']}")
        if isinstance(inst, (int, float)) and not isinstance(inst, bool):
            if "minimum" in s and inst < s["minimum"]:
                errs.append(f"{path}: {inst} < minimum {s['minimum']}")
            if "maximum" in s and inst > s["maximum"]:
                errs.append(f"{path}: {inst} > maximum {s['maximum']}")
        if isinstance(inst, dict):
            for r in s.get("required", []):
                if r not in inst:
                    errs.append(f"{path}: missing required {r!r}")
        if isinstance(inst, list) and "items" in s:
            for i, item in enumerate(inst):
                check(item, s["items"], b, f"{path}[{i}]", errs)
        if isinstance(inst, dict) and isinstance(ap, dict):
            for k, v in inst.items():
                if k not in props:
                    check(v, ap, b, f"{path}.{k}", errs)
    if isinstance(inst, dict) and props:
        for k, v in inst.items():
            if k in props:
                check(v, props[k][0], props[k][1], f"{path}.{k}", errs)
            elif not open_object:
                errs.append(f"{path}: unknown key {k!r}")


def openapi(spec: str, definition: str, instance_path: str) -> int:
    doc = load(spec)
    defs = doc.get("definitions") or doc.get("components", {}).get("schemas", {})
    if definition not in defs:
        print(f"FAIL {instance_path}: no definition {definition} in {spec}")
        return 1
    inst = yaml.safe_load(Path(instance_path).read_text())
    errs: list[str] = []
    check(inst, defs[definition], spec, "$", errs)
    for e in errs:
        print(f"FAIL {instance_path} {e}")
    if not errs:
        print(f"ok   {instance_path} ({definition})")
    return 1 if errs else 0


def boto(service: str, operation: str, params_path: str) -> int:
    import botocore.session
    import botocore.validate

    model = botocore.session.get_session().get_service_model(service)
    shape = model.operation_model(operation).input_shape
    report = botocore.validate.ParamValidator().validate(json.loads(Path(params_path).read_text()), shape)
    if report.has_errors():
        print(f"FAIL {params_path} ({service} {operation})\n{report.generate_report()}")
        return 1
    print(f"ok   {params_path} ({service} {operation})")
    return 0


SECRET_HEX = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
SECRETS = ("XI_SIGNING_KEYS", "XI_SIGNING_SALTS", "XI_VIDEO_TRANSFORM_STREAM_KEYS")


def _pairs(kind: str, doc: Any) -> list[list[tuple[str, str]]]:
    """Per container/service: the XI_* variables a platform file sets (literal values only)."""
    out: list[list[tuple[str, str]]] = []

    def add(items: list[tuple[str, Any]]) -> None:
        out.append([(k, "" if v is None else str(v)) for k, v in items if k.startswith("XI_")])

    if kind == "compose":  # `docker compose config --format json`
        for svc in doc.get("services", {}).values():
            add(list((svc.get("environment") or {}).items()))
    elif kind == "fly":
        add(list(doc.get("env", {}).items()))
    elif kind == "knative":
        for c in doc["spec"]["template"]["spec"]["containers"]:
            add([(e["name"], e.get("value", "")) for e in c.get("env", [])])
    elif kind == "aca":
        for c in doc["properties"]["template"]["containers"]:
            add([(e["name"], e.get("value", "")) for e in c.get("env", [])])
    elif kind == "render":
        for s in doc.get("services", []):
            add([(e["key"], e.get("value", "")) for e in s.get("envVars", [])])
    elif kind == "do":
        for s in doc.get("services", []):
            add([(e["key"], e.get("value", "")) for e in s.get("envs", [])])
    elif kind == "ecs":
        for c in doc["containerDefinitions"]:
            add([(e["name"], e["value"]) for e in c.get("environment", [])] + [(e["name"], "") for e in c.get("secrets", [])])
    elif kind == "apprunner":
        img = doc["SourceConfiguration"]["ImageRepository"]["ImageConfiguration"]
        add(list(img.get("RuntimeEnvironmentVariables", {}).items()) + [(k, "") for k in img.get("RuntimeEnvironmentSecrets", {})])
    else:
        raise ValueError(f"unknown kind {kind}")
    return out


def env(kind: str, path: str, outdir: str) -> int:
    text = Path(path).read_text()
    if kind == "fly":
        import tomllib

        doc = tomllib.loads(text)
    else:
        doc = yaml.safe_load(text)
    for i, pairs in enumerate(_pairs(kind, doc)):
        if not pairs:
            continue
        lines = []
        for k, v in pairs:
            if k in SECRETS or not v or v.startswith("replace-with"):
                v = SECRET_HEX if k in SECRETS else v
            lines.append(f"{k}={v}")
        if not any(k == "XI_SIGNING_KEYS" for k, _ in pairs):
            lines += [f"XI_SIGNING_KEYS={SECRET_HEX}", f"XI_SIGNING_SALTS={SECRET_HEX}"]
        Path(outdir, f"{i}.env").write_text("\n".join(lines) + "\n")
    return 0


def main(argv: list[str]) -> int:
    if len(argv) == 5 and argv[1] == "env":
        return env(argv[2], argv[3], argv[4])
    if len(argv) == 5 and argv[1] == "openapi":
        return openapi(argv[2], argv[3], argv[4])
    if len(argv) == 5 and argv[1] == "botocore":
        return boto(argv[2], argv[3], argv[4])
    print(__doc__)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
