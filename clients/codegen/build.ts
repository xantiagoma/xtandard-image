// The language-neutral model of the spec (WP-146): what every refiner reads
// before it lowers the spec into its language's nodes. It holds data, no
// syntax: the three catalogues with their options (names, aliases, short
// names, argument shapes, bare arguments, documentation tags), the name
// tables, the enumerations and constants of the spec, and the response
// schemas with their properties in a stable order. lib.ts keeps the shape
// rules it is built from.

import { allArgs, bareArgs, nameTable, type Shape, shapes } from "./lib.ts";
import { type CatalogueKind, catalogueKinds, type JSONSchema, type Spec, type SpecArg, type SpecOption } from "./spec.ts";

/** Compares strings by UTF-16 code units, as every emitter sorts. */
export const byCode = (a: string, b: string): number => (a < b ? -1 : a > b ? 1 : 0);

/** One option of a catalogue, with what the refiners derive from it. */
export interface OptionModel {
  readonly spec: SpecOption;
  readonly kind: CatalogueKind;
  readonly name: string;
  readonly aliases: readonly string[];
  /** The canonical name, then the aliases. */
  readonly names: readonly string[];
  readonly short: string;
  /** The argument lists the option accepts (lib.ts shapes). */
  readonly shapes: readonly Shape[];
  /** The argument types it may be written with as a bare value. */
  readonly bare: readonly SpecArg[];
  /** Every argument its forms may contain. */
  readonly args: readonly SpecArg[];
  /** milestone, scope and flags as documentation phrases ("scope pipeline", …). */
  readonly tags: readonly string[];
}

/** A catalogue (processing, info, video). */
export interface CatalogueModel {
  readonly kind: CatalogueKind;
  readonly options: readonly OptionModel[];
  /** Every name and alias with its canonical name, sorted by name. */
  readonly names: readonly (readonly [string, string])[];
  readonly byName: ReadonlyMap<string, OptionModel>;
}

/** The documentation phrases of an option's flags, in the order the clients write them. */
export function optionTags(o: SpecOption): string[] {
  const tags: string[] = [];
  if (o.milestone) tags.push(`milestone ${o.milestone}`);
  tags.push(`scope ${o.scope}`);
  if (o.pro) tags.push("imgproxy Pro option (free here)");
  if (o.security) tags.push("security option (needs XI_SECURITY_ALLOW_SECURITY_OPTIONS)");
  if (o.removed) tags.push("removed: accepted and ignored");
  return tags;
}

/** The option model of one spec option. */
export function optionModel(kind: CatalogueKind, o: SpecOption): OptionModel {
  return {
    spec: o,
    kind,
    name: o.name,
    aliases: o.aliases,
    names: [o.name, ...o.aliases],
    short: o.short,
    shapes: shapes(o),
    bare: bareArgs(o),
    args: allArgs(o),
    tags: optionTags(o),
  };
}

/** The catalogue model of one kind. */
export function catalogue(spec: Spec, kind: CatalogueKind): CatalogueModel {
  const options = spec.options[kind].map((o) => optionModel(kind, o));
  return { kind, options, names: nameTable(spec.options[kind]), byName: new Map(options.map((o) => [o.name, o])) };
}

/** The three catalogues, in the order generators emit them. */
export function catalogues(spec: Spec): CatalogueModel[] {
  return catalogueKinds.map((k) => catalogue(spec, k));
}

/** A string enumeration of the spec, named in snake_case words. */
export interface EnumModel {
  readonly name: string;
  readonly values: readonly string[];
}

/** The string enumerations of the spec. */
export function enums(spec: Spec): Record<
  "image_format" | "video_container" | "stream_protocol" | "segment_format" | "source_scheme" | "source_encoding" | "imaginary_endpoint" | "error_kind",
  EnumModel
> {
  const e = (name: string, values: readonly string[]): EnumModel => ({ name, values });
  return {
    image_format: e("image_format", spec.media.image_formats),
    video_container: e("video_container", spec.media.video_containers),
    stream_protocol: e("stream_protocol", spec.media.streams.protocols.map((p) => p.name)),
    segment_format: e("segment_format", spec.media.streams.segment_formats.map((f) => f.name)),
    source_scheme: e("source_scheme", spec.sources.schemes.map((s) => s.scheme)),
    source_encoding: e("source_encoding", spec.sources.encodings),
    imaginary_endpoint: e("imaginary_endpoint", spec.query.imaginary.endpoints),
    error_kind: e("error_kind", spec.error_kinds.map((k) => k.kind)),
  };
}

/** imaginary parameters the query builder writes itself (never a typed parameter). */
export const builderImaginaryParams: ReadonlySet<string> = new Set(["url", "sign", "xi_sig"]);

/** The imaginary parameters a caller may set. */
export function imaginaryParams(spec: Spec): string[] {
  return spec.query.imaginary.params.filter((p) => !builderImaginaryParams.has(p));
}

/** Every native /q/ image key and alias, in spec order. */
export function queryKeyNames(spec: Spec): string[] {
  return spec.query.image_keys.flatMap((k) => [k.name, ...k.aliases]);
}

/** The default segment format's name. */
export function defaultSegmentFormat(spec: Spec): string {
  const f = spec.media.streams.segment_formats.find((x) => x.default);
  if (!f) throw new Error("spec: no default segment format");
  return f.name;
}

/** The native /q/video keys, sorted by option name. */
export function nativeVideoKeys(spec: Spec): [string, string][] {
  return Object.entries(spec.query.native_video_keys).sort(([a], [b]) => byCode(a, b));
}

// ---- response schemas ----------------------------------------------------------------

/** One response schema, with its properties sorted by name. */
export interface SchemaModel {
  readonly name: string;
  readonly schema: JSONSchema;
  readonly properties: readonly (readonly [string, JSONSchema])[];
  readonly required: ReadonlySet<string>;
}

/** The response schemas sorted by name. */
export function responseSchemas(spec: Spec): SchemaModel[] {
  return Object.keys(spec.response_schemas)
    .sort(byCode)
    .map((name) => {
      const schema = spec.response_schemas[name]!;
      return {
        name,
        schema,
        properties: Object.entries(schema.properties ?? {}).sort(([a], [b]) => byCode(a, b)),
        required: new Set(schema.required ?? []),
      };
    });
}

/** The schema name of a $ref ("#/components/schemas/InfoRGB" → "InfoRGB"). */
export const refName = (ref: string): string => ref.replace("#/components/schemas/", "");

/** A schema's JSON types as a list (type may be a string, a list or absent). */
export function schemaTypes(s: JSONSchema): string[] {
  return s.type === undefined ? [] : Array.isArray(s.type) ? s.type : [s.type];
}
