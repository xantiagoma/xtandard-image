import { type ImgproxyInfoArgs, type ImgproxyProcessingArgs, type ImgproxyVideoArgs, imgproxyInfoAliases, imgproxyProcessingAliases, imgproxyVideoAliases } from "./generated/options.ts";
export type { ImgproxyInfoArgs, ImgproxyProcessingArgs, ImgproxyVideoArgs };
type ProcessingAliases = typeof imgproxyProcessingAliases;
type InfoAliases = typeof imgproxyInfoAliases;
type VideoAliases = typeof imgproxyVideoAliases;
/** Every processing option name: canonical names and aliases (`resize`, `rs`, …). */
export type ImgproxyProcessingOptionName = keyof ImgproxyProcessingArgs | keyof ProcessingAliases;
/** Every info option name: canonical names and aliases. */
export type ImgproxyInfoOptionName = keyof ImgproxyInfoArgs | keyof InfoAliases;
/** Every `/video` option name: canonical names and aliases (`start`, `vs`, …). */
export type ImgproxyVideoOptionName = keyof ImgproxyVideoArgs | keyof VideoAliases;
/** The argument tuple of a processing option, by canonical name or alias. */
export type ImgproxyProcessingArgsOf<K extends ImgproxyProcessingOptionName> = K extends keyof ImgproxyProcessingArgs ? ImgproxyProcessingArgs[K] : K extends keyof ProcessingAliases ? ImgproxyProcessingArgs[ProcessingAliases[K]] : never;
/** The argument tuple of an info option, by canonical name or alias. */
export type ImgproxyInfoArgsOf<K extends ImgproxyInfoOptionName> = K extends keyof ImgproxyInfoArgs ? ImgproxyInfoArgs[K] : K extends keyof InfoAliases ? ImgproxyInfoArgs[InfoAliases[K]] : never;
/** The argument tuple of a `/video` option, by canonical name or alias. */
export type ImgproxyVideoArgsOf<K extends ImgproxyVideoOptionName> = K extends keyof ImgproxyVideoArgs ? ImgproxyVideoArgs[K] : K extends keyof VideoAliases ? ImgproxyVideoArgs[VideoAliases[K]] : never;
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
 * Video transformation options (`/video`, WP-122, WP-123), keyed by
 * canonical name or alias. `{ rs: ["fill", 640, 360], vs: 1, vd: 5 }` is
 * `/rs:fill:640:360/vs:1/vd:5/`.
 */
export type ImgproxyVideoOptions = {
    [K in ImgproxyVideoOptionName]?: OptionValue<ImgproxyVideoArgsOf<K>>;
};
/**
 * How option names are written: the shortest alias (default), the
 * canonical name, or the name as given (a canonical name or an alias).
 */
export type OptionNameStyle = "short" | "canonical" | "given";
/** A single argument as written in a URL. */
export type Arg = string | number | boolean | null | undefined;
interface Catalogue {
    readonly canonical: ReadonlyMap<string, string>;
    readonly short: ReadonlyMap<string, string>;
}
export declare const processingCatalogue: Catalogue;
export declare const infoCatalogue: Catalogue;
export declare const videoCatalogue: Catalogue;
/** One option occurrence: a name (canonical or alias) and its arguments. */
export type OptionEntry = readonly [name: string, args: readonly Arg[]];
/** Converts an options object into entries, in key order. */
export declare function entriesOf(options: object): OptionEntry[];
/**
 * Formats one argument. Strings are checked, never escaped: the server does
 * not unescape option arguments (color_profile file names excepted, which
 * callers percent-escape themselves).
 */
export declare function formatArg(a: Arg, sep: string, where: string): string;
/** Formats one option segment ("rs:fill:300:400"). */
export declare function formatEntry(cat: Catalogue, [name, args]: OptionEntry, sep: string, style: OptionNameStyle): string;
