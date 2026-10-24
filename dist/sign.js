// URL signing with WebCrypto HMAC-SHA256, byte-compatible with
// internal/security/sign:
//
//   imgproxy  base64url(HMAC-SHA256(key, salt ‖ path)[:size]), the signature
//             segment of /{signature}/{options}/{source}.
//   xi_sig    the same construction over path ‖ "?" ‖ query-without-xi_sig,
//             appended as the last query parameter (docs/URL-DIALECTS.md §2).
import { base64url, concat, keyBytes, subtle, utf8 } from "./bytes.js";
/** The query parameter that carries an xi_sig signature. */
export const XI_SIG_PARAM = "xi_sig";
const MAX_SIZE = 32;
// Imported HMAC keys, per SigningKey object: signing many URLs with one key
// imports it once.
const keyCache = new WeakMap();
function hmacKey(k) {
    let p = keyCache.get(k);
    if (!p) {
        p = subtle().importKey("raw", keyBytes(k.key), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
        keyCache.set(k, p);
    }
    return p;
}
function checkSize(size) {
    if (!Number.isInteger(size) || size < 1 || size > MAX_SIZE) {
        throw new RangeError(`signature size ${size} out of range [1, ${MAX_SIZE}]`);
    }
    return size;
}
/**
 * Signs msg with the salted HMAC-SHA256 construction shared by the imgproxy
 * and xi_sig schemes. Like the server, a msg without a leading "/" is
 * signed as if it had one.
 */
export async function sign(msg, k) {
    const size = checkSize(k.size ?? MAX_SIZE);
    const m = msg.startsWith("/") ? msg : `/${msg}`;
    const mac = await subtle().sign("HMAC", await hmacKey(k), concat(keyBytes(k.salt), utf8(m)));
    return base64url(new Uint8Array(mac, 0, size));
}
/**
 * Returns "/" + signature + path for an imgproxy path (everything after the
 * signature segment: "/rs:fill:300:400/plain/…"), like the server's
 * Signer.SignImgproxyPath.
 */
export async function signImgproxyPath(path, k) {
    const p = path.startsWith("/") ? path : `/${path}`;
    return `/${await sign(p, k)}${p}`;
}
/**
 * Splits a raw query into the xi_sig value and the message xi_sig signs:
 * path, then "?" and the remaining non-empty parameters in their original
 * order and encoding. Throws on a repeated xi_sig (the server answers 403).
 */
export function xiMessage(path, rawQuery) {
    let sig;
    const kept = [];
    for (const part of rawQuery.split("&")) {
        if (part === "")
            continue;
        const eq = part.indexOf("=");
        const key = eq < 0 ? part : part.slice(0, eq);
        if (key === XI_SIG_PARAM) {
            if (sig !== undefined)
                throw new Error(`query has more than one ${XI_SIG_PARAM} parameter`);
            sig = eq < 0 ? "" : part.slice(eq + 1);
            continue;
        }
        kept.push(part);
    }
    return { sig, msg: kept.length ? `${path}?${kept.join("&")}` : path };
}
/**
 * Returns rawQuery with any xi_sig removed and a fresh xi_sig for path
 * appended as the last parameter, like the server's Signer.SignXIQuery.
 * path is the escaped path the dialect sees (after its mount prefix).
 */
export async function signXiQuery(path, rawQuery, k) {
    const { msg } = xiMessage(path, rawQuery.startsWith("?") ? rawQuery.slice(1) : rawQuery);
    const sig = await sign(msg, k);
    const q = msg.slice(path.length);
    return q === "" ? `${XI_SIG_PARAM}=${sig}` : `${q.slice(1)}&${XI_SIG_PARAM}=${sig}`;
}
/**
 * Signs a URL (absolute, or a path with an optional query) with xi_sig.
 * `mount` is the dialect's mount prefix, which the signed path excludes
 * (for example "/q" for the query dialect, docs/URL-DIALECTS.md §4).
 */
export async function signXiUrl(url, k, opts = {}) {
    const s = String(url);
    const hash = s.indexOf("#");
    const noHash = hash < 0 ? s : s.slice(0, hash);
    const qi = noHash.indexOf("?");
    const head = qi < 0 ? noHash : noHash.slice(0, qi);
    const query = qi < 0 ? "" : noHash.slice(qi + 1);
    const pathStart = /^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(head) ? head.indexOf("/", head.indexOf("//") + 2) : 0;
    const origin = pathStart < 0 ? head : head.slice(0, pathStart);
    let path = pathStart < 0 ? "/" : head.slice(pathStart);
    const mount = opts.mount?.replace(/\/+$/, "") ?? "";
    if (mount !== "") {
        if (path !== mount && !path.startsWith(`${mount}/`)) {
            throw new Error(`path ${path} is not under the mount ${mount}`);
        }
        path = path.slice(mount.length) || "/";
    }
    const signed = await signXiQuery(path, query, k);
    return `${origin}${mount}${path}?${signed}`;
}
