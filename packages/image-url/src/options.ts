// The option types the URL builder is typed by, derived from the generated
// catalogue (src/generated/options.ts, from the OpenAPI
// ImgproxyProcessingOptions / ImgproxyInfoOptions schemas), and their
// serialisation into imgproxy option segments.

import {
  type ImgproxyInfoArgs,
  type ImgproxyProcessingArgs,
  imgproxyInfoAliases,
  imgproxyInfoOptionNames,
  imgproxyProcessingAliases,
  imgproxyProcessingOptionNames,
} from "./generated/options.ts";

export type { ImgproxyInfoArgs, ImgproxyProcessingArgs };

type ProcessingAliases = typeof imgproxyProcessingAliases;
type InfoAliases = typeof imgproxyInfoAliases;

/** Every processing option name: canonical names and aliases (`resize`, `rs`, …). */
export type ImgproxyProcessingOptionName = keyof ImgproxyProcessingArgs | keyof ProcessingAliases;

/** Every info option name: canonical names and aliases. */
export type ImgproxyInfoOptionName = keyof ImgproxyInfoArgs | keyof InfoAliases;

/** The argument tuple of a processing option, by canonical name or alias. */
export type ImgproxyProcessingArgsOf<K extends ImgproxyProcessingOptionName> = K extends keyof ImgproxyProcessingArgs
  ? ImgproxyProcessingArgs[K]
  : K extends keyof ProcessingAliases
    ? ImgproxyProcessingArgs[ProcessingAliases[K]]
    : never;

/** The argument tuple of an info option, by canonical name or alias. */
export type ImgproxyInfoArgsOf<K extends ImgproxyInfoOptionName> = K extends keyof ImgproxyInfoArgs
  ? ImgproxyInfoArgs[K]
  : K extends keyof InfoAliases
    ? ImgproxyInfoArgs[InfoAliases[K]]
    : never;

/**
 * An option value: its argument tuple, or, when every argument after the
 * first is optional, the first argument alone (`quality: 80` is
 * `quality: [80]`).
 */
export type OptionValue<T> = Readonly<T> | FirstArg<T>;

type FirstArg<T> = T extends readonly [infer A, ...infer Rest] ? ([] extends Rest ? Exclude<A, null> : never) : never;

/**
 * imgproxy processing options for one pipeline, keyed by canonical name or
 * alias. `{ resize: ["fill", 300, 400], q: 80 }` is `/rs:fill:300:400/q:80/`.
 * Keys are written in insertion order; `undefined` values are skipped.
 */
export type ImgproxyProcessingOptions = {
  [K in ImgproxyProcessingOptionName]?: OptionValue<ImgproxyProcessingArgsOf<K>>;
};

/** imgproxy `/info` options, keyed by canonical name or alias. */
export type ImgproxyInfoOptions = {
  [K in ImgproxyInfoOptionName]?: OptionValue<ImgproxyInfoArgsOf<K>>;
};

/**
 * How option names are written: the shortest alias (default), the
 * canonical name, or the name as given (a canonical name or an alias).
 */
export type OptionNameStyle = "short" | "canonical" | "given";

/** A single argument as written in a URL. */
export type Arg = string | number | boolean | null | undefined;

interface Catalogue {
  readonly canonical: ReadonlyMap<string, string>; // name or alias → canonical
  readonly short: ReadonlyMap<string, string>; // canonical → shortest name
}

function catalogue(names: readonly string[], aliases: Readonly<Record<string, string>>): Catalogue {
  const canonical = new Map<string, string>(names.map((n) => [n, n]));
  const short = new Map<string, string>(names.map((n) => [n, n]));
  for (const [alias, name] of Object.entries(aliases)) {
    canonical.set(alias, name);
    const cur = short.get(name) ?? name;
    if (alias.length < cur.length) short.set(name, alias);
  }
  return { canonical, short };
}

export const processingCatalogue: Catalogue = catalogue(imgproxyProcessingOptionNames, imgproxyProcessingAliases);
export const infoCatalogue: Catalogue = catalogue(imgproxyInfoOptionNames, imgproxyInfoAliases);

/** One option occurrence: a name (canonical or alias) and its arguments. */
export type OptionEntry = readonly [name: string, args: readonly Arg[]];

/** Converts an options object into entries, in key order. */
export function entriesOf(options: object): OptionEntry[] {
  const out: OptionEntry[] = [];
  for (const [name, value] of Object.entries(options) as [string, unknown][]) {
    if (value === undefined) continue;
    out.push([name, Array.isArray(value) ? (value as Arg[]) : [value as Arg]]);
  }
  return out;
}

/**
 * Formats one argument. Strings are checked, never escaped: the server does
 * not unescape option arguments (color_profile file names excepted, which
 * callers percent-escape themselves).
 */
export function formatArg(a: Arg, sep: string, where: string): string {
  switch (typeof a) {
    case "undefined":
      return "";
    case "boolean":
      return a ? "1" : "0";
    case "number":
      if (!Number.isFinite(a)) throw new RangeError(`${where}: ${a} is not a finite number`);
      return String(a);
    case "string":
      if (a.includes("/") || a.includes(sep) || /[?#\s\u0000-\u001f\u007f]/.test(a)) {
        throw new TypeError(`${where}: argument ${JSON.stringify(a)} contains a character that cannot appear in an option`);
      }
      return a;
    default:
      if (a === null) return "";
      throw new TypeError(`${where}: unsupported argument type ${typeof a}`);
  }
}

/** Formats one option segment ("rs:fill:300:400"). */
export function formatEntry(cat: Catalogue, [name, args]: OptionEntry, sep: string, style: OptionNameStyle): string {
  const canonical = cat.canonical.get(name);
  if (canonical === undefined) throw new TypeError(`unknown imgproxy option ${JSON.stringify(name)}`);
  if (args.length === 0) throw new TypeError(`option ${name}: at least one argument is required`);
  const written = style === "given" ? name : style === "canonical" ? canonical : (cat.short.get(canonical) ?? canonical);
  return [written, ...args.map((a, i) => formatArg(a, sep, `option ${name} argument ${i + 1}`))].join(sep);
}
