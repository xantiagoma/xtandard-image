// The option types the URL builder is typed by, derived from the generated
// catalogue (src/generated/options.ts, from the OpenAPI
// ImgproxyProcessingOptions / ImgproxyInfoOptions / ImgproxyVideoOptions
// schemas), and their serialisation into imgproxy option segments.
import { imgproxyInfoAliases, imgproxyInfoOptionNames, imgproxyProcessingAliases, imgproxyProcessingOptionNames, imgproxyVideoAliases, imgproxyVideoOptionNames, } from "./generated/options.js";
function catalogue(names, aliases) {
    const canonical = new Map(names.map((n) => [n, n]));
    const short = new Map(names.map((n) => [n, n]));
    for (const [alias, name] of Object.entries(aliases)) {
        canonical.set(alias, name);
        const cur = short.get(name) ?? name;
        if (alias.length < cur.length)
            short.set(name, alias);
    }
    return { canonical, short };
}
export const processingCatalogue = catalogue(imgproxyProcessingOptionNames, imgproxyProcessingAliases);
export const infoCatalogue = catalogue(imgproxyInfoOptionNames, imgproxyInfoAliases);
export const videoCatalogue = catalogue(imgproxyVideoOptionNames, imgproxyVideoAliases);
/** Converts an options object into entries, in key order. */
export function entriesOf(options) {
    const out = [];
    for (const [name, value] of Object.entries(options)) {
        if (value === undefined)
            continue;
        out.push([name, Array.isArray(value) ? value : [value]]);
    }
    return out;
}
/**
 * Formats one argument. Strings are checked, never escaped: the server does
 * not unescape option arguments (color_profile file names excepted, which
 * callers percent-escape themselves).
 */
export function formatArg(a, sep, where) {
    switch (typeof a) {
        case "undefined":
            return "";
        case "boolean":
            return a ? "1" : "0";
        case "number":
            if (!Number.isFinite(a))
                throw new RangeError(`${where}: ${a} is not a finite number`);
            return String(a);
        case "string":
            if (a.includes("/") || a.includes(sep) || /[?#\s\u0000-\u001f\u007f]/.test(a)) {
                throw new TypeError(`${where}: argument ${JSON.stringify(a)} contains a character that cannot appear in an option`);
            }
            return a;
        default:
            if (a === null)
                return "";
            throw new TypeError(`${where}: unsupported argument type ${typeof a}`);
    }
}
/** Formats one option segment ("rs:fill:300:400"). */
export function formatEntry(cat, [name, args], sep, style) {
    const canonical = cat.canonical.get(name);
    if (canonical === undefined)
        throw new TypeError(`unknown imgproxy option ${JSON.stringify(name)}`);
    if (args.length === 0)
        throw new TypeError(`option ${name}: at least one argument is required`);
    const written = style === "given" ? name : style === "canonical" ? canonical : (cat.short.get(canonical) ?? canonical);
    return [written, ...args.map((a, i) => formatArg(a, sep, `option ${name} argument ${i + 1}`))].join(sep);
}
