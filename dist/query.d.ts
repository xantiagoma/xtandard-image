import { type EncryptionKey } from "./encrypt.ts";
import type { operations } from "./generated/openapi.ts";
import { type OptionEntry, type OptionNameStyle } from "./options.ts";
import { type SigningKey } from "./sign.ts";
/** Settings of query dialect URLs. */
export interface QueryUrlConfig {
    /** Server origin (and any path before the mount). Default "" (root-relative URLs). */
    readonly baseUrl?: string;
    /** The query dialect's mount prefix (XI_DIALECTS_QUERY_PREFIX). Default "/q". */
    readonly mount?: string;
    /** Signs with xi_sig (the imgproxy key and salt). Without it the URL is unsigned. */
    readonly signing?: SigningKey;
    /** Key for `pdfPassword` (XI_ENCRYPTION_KEYS). */
    readonly encryption?: EncryptionKey;
    /** Argument separator inside `o=` (XI_DIALECTS_IMGPROXY_ARGUMENTS_SEPARATOR). Default ":". */
    readonly separator?: string;
    /** Option names in `o=` and video keys: the shortest alias (default), the canonical name, or as given. */
    readonly optionNames?: OptionNameStyle;
}
/** A native query key value; booleans are written `true`/`false`. */
export type QueryValue = string | number | boolean;
type QueryOf<Op extends keyof operations> = operations[Op] extends {
    parameters: {
        query?: infer Q;
    };
} ? NonNullable<Q> : never;
type Params<Q, Drop extends string> = {
    [K in Exclude<keyof Q, Drop>]?: QueryValue | null;
};
/**
 * Native keys of `/q/` (getQueryImage in the OpenAPI document): `w`,
 * `fit`, `g`, `f`, `q`, `preset`, `vts`, `pages`, … `url`, `o`, `sign`,
 * `xi_sig` and `pdf_password` are written by the builder.
 */
export type QueryImageParams = Params<QueryOf<"getQueryImage">, "url" | "o" | "sign" | "xi_sig" | "pdf_password" | "pdp">;
/** imaginary endpoints of `/q/<endpoint>` (getImaginaryImage). */
export type ImaginaryEndpoint = operations["getImaginaryImage"]["parameters"]["path"]["endpoint"];
/** imaginary parameters of `/q/<endpoint>` (getImaginaryImage). */
export type ImaginaryParams = Params<QueryOf<"getImaginaryImage">, "url" | "sign" | "xi_sig">;
/** Percent-escapes a query value (keeps `:`, `/` and `,` readable). */
export declare function escapeQueryValue(v: string): string;
/** Native keys: an object (key order) or [key, value] pairs (keys may repeat). */
export type ParamList = object | readonly (readonly [string, QueryValue])[];
/** Internal: a native `/q/` image URL; `o=` carries the option pipelines. */
export declare function buildQueryImage(cfg: QueryUrlConfig, pipelines: readonly (readonly OptionEntry[])[], source: string, params: ParamList | undefined, ext: string | undefined, pdfPassword: string | undefined): Promise<string>;
/** Internal: a `/q/video` URL; every video option is a key, its arguments joined by ",". */
export declare function buildQueryVideo(cfg: QueryUrlConfig, entries: readonly OptionEntry[], source: string, ext: string | undefined): Promise<string>;
/** Internal: an imaginary endpoint URL (`/q/resize?url=…&width=300`). */
export declare function buildImaginary(cfg: QueryUrlConfig, endpoint: string, source: string, params: ParamList | undefined): Promise<string>;
/** The media type of a QUERY request's content: the URL's query string (RFC 10008). */
export declare const QUERY_CONTENT_TYPE = "application/x-www-form-urlencoded";
/** A query dialect request sent with the HTTP QUERY method (RFC 10008, URL-DIALECTS §4.4). */
export interface QueryMethodRequest {
    readonly method: "QUERY";
    /** The URL without its query. */
    readonly url: string;
    readonly contentType: typeof QUERY_CONTENT_TYPE;
    /**
     * The URL's query exactly as built, `xi_sig` included: the server signs,
     * limits and caches it as the GET URL, since `xi_sig` does not cover the method.
     */
    readonly body: string;
}
/**
 * Splits a built query dialect URL (`/q/…?url=…`) into an HTTP QUERY
 * request: the parameters move from the URL to the content, so long
 * option chains, watermark text or encrypted sources stay out of URLs.
 * The server must have XI_SERVER_QUERY_METHOD on (the default); many CDNs
 * and proxies neither cache nor forward QUERY.
 */
export declare function queryMethodRequest(url: string): QueryMethodRequest;
export {};
