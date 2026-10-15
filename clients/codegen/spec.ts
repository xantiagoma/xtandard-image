// Types of clients/spec/spec.json, the language-neutral client
// specification written by internal/apidoc (BuildClientSpec, ADR 0013).
// Field documentation lives on the Go structs (internal/apidoc/clientspec.go).

export interface SpecArg {
  name: string;
  /** string, integer, number, boolean, enum, color, base64 or direction. */
  type: "string" | "integer" | "number" | "boolean" | "enum" | "color" | "base64" | "direction";
  enum?: string[];
  min?: number;
  max?: number;
  exclusive_min?: boolean;
  multiple_of?: number;
  pattern?: string;
  nullable: boolean;
  doc?: string;
}

export interface SpecForm {
  args: SpecArg[];
  rest?: SpecArg[];
  min_items: number;
}

export interface SpecOption {
  name: string;
  aliases: string[];
  short: string;
  doc: string;
  syntax: string;
  default: string;
  milestone: string;
  scope: "pipeline" | "request";
  pro?: boolean;
  security?: boolean;
  removed?: boolean;
  shared?: boolean;
  example?: unknown[];
  forms: SpecForm[];
}

export interface JSONSchema {
  type?: string | string[];
  enum?: unknown[];
  $ref?: string;
  description?: string;
  properties?: Record<string, JSONSchema>;
  required?: string[];
  additionalProperties?: boolean | JSONSchema;
  items?: JSONSchema;
  minItems?: number;
  maxItems?: number;
  minimum?: number;
  maximum?: number;
}

export interface Spec {
  $comment: string;
  spec_version: number;
  options: { processing: SpecOption[]; info: SpecOption[]; video: SpecOption[] };
  query: {
    mount: string;
    image_keys: { name: string; aliases: string[]; doc: string }[];
    builder_keys: string[];
    native_video_keys: Record<string, string>;
    imaginary: { endpoints: string[]; params: string[] };
  };
  media: {
    image_formats: string[];
    video_containers: string[];
    streams: {
      protocols: { name: string; container: string; content_type: string }[];
      segment_formats: { name: string; aliases: string[]; protocols: string[]; default?: boolean }[];
      max_renditions: number;
      rendition_min: number;
      rendition_max: number;
      segment_duration_min: number;
      segment_duration_max: number;
    };
  };
  sources: {
    schemes: { scheme: string; authority: string; example: string }[];
    xi_name_pattern: string;
    encodings: string[];
  };
  signing: {
    algorithm: string;
    encoding: string;
    min_size: number;
    max_size: number;
    default_size: number;
    unsigned_placeholders: string[];
    default_unsigned: string;
    query_param: string;
    rules: Record<string, string>;
  };
  encryption: {
    cipher: string;
    key_sizes: number[];
    iv_size: number;
    token: string;
    source_prefix: string;
    max_pdf_password_bytes: number;
    pdf_password_names: string[];
    pdf_password_short_name: string;
    rules: string[];
  };
  url: {
    default_separator: string;
    forbidden_separators: string[];
    forbidden_query_separators: string[];
    pipeline_separator: string;
    plain_safe: string;
    query_safe: string;
    extension_pattern: string;
    rules: string[];
  };
  error_kinds: { kind: string; status: number; message: string }[];
  response_schemas: Record<string, JSONSchema>;
  endpoints: Record<string, string>;
}

/** The catalogue kinds, in the order generators emit them. */
export const catalogueKinds = ["processing", "info", "video"] as const;
export type CatalogueKind = (typeof catalogueKinds)[number];

/** What a language emitter (clients/codegen/emitters/<lang>.ts) exports as `emitter`. */
export interface Emitter {
  /** The language, also the clients/<language>/ directory. */
  readonly language: string;
  /** Files to write, by path relative to the repository root, from the spec. */
  emit(spec: Spec): Record<string, string>;
}
