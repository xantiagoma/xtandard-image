// A small spec for the golden tests (WP-146): clients/spec/spec.json cut
// down to a few options per catalogue that between them have every
// argument type (string, integer, number, boolean, enum, color, base64,
// direction), nullable arguments, rest groups, several forms, aliases, a
// name with a dash and one without aliases, and fewer query keys,
// imaginary parameters, sources and response schemas. Everything else is the real spec, so
// the goldens change when a client's output for it does.

import type { Spec } from "../spec.ts";

const keep: Record<keyof Spec["options"], readonly string[]> = {
  processing: ["crop", "dpi", "gradient", "min-width", "resize", "watermark_url"],
  info: ["cache_tags", "size"],
  video: ["crop", "segment_format", "stream"],
};

/** The small spec. */
export async function miniSpec(): Promise<Spec> {
  const spec = (await Bun.file(new URL("../../spec/spec.json", import.meta.url)).json()) as Spec;
  for (const kind of ["processing", "info", "video"] as const) {
    spec.options[kind] = spec.options[kind].filter((o) => keep[kind].includes(o.name));
    if (spec.options[kind].length !== keep[kind].length) throw new Error(`miniSpec: the spec lost a ${kind} option of the fixture`);
  }
  spec.query.image_keys = spec.query.image_keys.slice(0, 4);
  spec.query.imaginary.params = spec.query.imaginary.params.slice(0, 6);
  spec.query.imaginary.endpoints = spec.query.imaginary.endpoints.slice(0, 3);
  spec.sources.schemes = spec.sources.schemes.slice(0, 3);
  // The error envelope (a $ref), and objects with nested refs, arrays and nullable fields.
  const schemas = ["ErrorBody", "ErrorResponse", "InfoDominant", "InfoObject", "InfoRGB"];
  spec.response_schemas = Object.fromEntries(Object.entries(spec.response_schemas).filter(([n]) => schemas.includes(n)));
  return spec;
}
