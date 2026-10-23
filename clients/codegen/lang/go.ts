// Go: leaf tokens and the printer (WP-146). Doc comments are "//" lines
// before the declaration; composite literals are Type{…}; a function's
// results are one type, (T, U) or named (a T, b U). The printed file goes
// through gofmt (emitters/go.ts), which aligns fields and const blocks, so
// the printer writes single spaces.
//
// Go's own nodes (ext kinds):
//   go.package  {name}                      package clause
//   go.imports  {specs: Import[]}           import ( … )
//   go.group    {keyword, doc?, items}      const ( … ) / var ( … )
//   go.slice    {elem}                      []T (a pointer *T is a nullable tref)
//   go.map      {key, value}                map[K]V
//   go.results  {params: Param[]}           named results (a T, b U)
//   go.trailing {node, text}                a declaration with a trailing comment

import { concat, type Doc, empty, hardline, indent, join, lines } from "../doc.ts";
import { words } from "../lib.ts";
import type * as M from "../model.ts";
import { isStmts, N } from "../model.ts";
import { Printer } from "../printer.ts";

/** Words written in upper case (Go initialisms) or with a fixed casing in identifiers. */
export const casing: Readonly<Record<string, string>> = {
  id: "ID",
  url: "URL",
  uri: "URI",
  pdf: "PDF",
  rgb: "RGB",
  rgba: "RGBA",
  hdr: "HDR",
  sdr: "SDR",
  dpi: "DPI",
  dpr: "DPR",
  fps: "FPS",
  bps: "BPS",
  crf: "CRF",
  icc: "ICC",
  exif: "EXIF",
  iptc: "IPTC",
  xmp: "XMP",
  css: "CSS",
  mime: "MIME",
  http: "HTTP",
  https: "HTTPS",
  ftp: "FTP",
  sftp: "SFTP",
  json: "JSON",
  md5: "MD5",
  sha1: "SHA1",
  sha256: "SHA256",
  sha512: "SHA512",
  dssim: "DSSIM",
  ml: "ML",
  jpeg: "JPEG",
  jpg: "JPG",
  png: "PNG",
  gif: "GIF",
  avif: "AVIF",
  jxl: "JXL",
  heic: "HEIC",
  tiff: "TIFF",
  bmp: "BMP",
  ico: "ICO",
  svg: "SVG",
  webp: "WebP",
  webm: "WebM",
  mp4: "MP4",
  mp3: "MP3",
  m4a: "M4A",
  wav: "WAV",
  ogg: "OGG",
  flac: "FLAC",
  aac: "AAC",
  pcm: "PCM",
  hevc: "HEVC",
  av1: "AV1",
  vp9: "VP9",
  h264: "H264",
  h265: "H265",
  hls: "HLS",
  dash: "DASH",
  m3u8: "M3U8",
  mpd: "MPD",
  ts: "TS",
  fmp4: "FMP4",
  cmaf: "CMAF",
  mpegts: "MPEGTS",
  s3: "S3",
  gs: "GS",
  abs: "ABS",
  xi: "Xi",
};

/** A Go exported identifier for a name ("min-width" → "MinWidth", "pdf_password" → "PDFPassword", "3d" → "V3d"). */
export function ident(name: string): string {
  const id = words(name)
    .map((w) => casing[w.toLowerCase()] ?? w[0]!.toUpperCase() + w.slice(1))
    .join("");
  if (id === "") throw new Error(`no identifier for ${JSON.stringify(name)}`);
  return /^[0-9]/.test(id) ? `V${id}` : id;
}

/** The lines of a Go comment: trimmed text, each line without trailing spaces. */
export function commentLines(text: readonly string[]): string[] {
  return text
    .join("\n")
    .trim()
    .split("\n")
    .map((l) => (l.trim() === "" ? "" : l.trimEnd()));
}

/** A struct tag key:"value" pair, as a raw string literal (`json:"name,omitempty"`). */
export function structTag(key: string, value: string): string {
  if (value.includes("`") || value.includes('"')) throw new Error(`go: struct tag value ${value} needs escaping`);
  return `\`${key}:"${value}"\``;
}

/** Go's own nodes. */
type GoNode =
  | { k: "go.package"; name: string }
  | { k: "go.imports"; specs: readonly M.Import[] }
  | { k: "go.group"; keyword: "const" | "var"; items: readonly M.Node[]; doc?: M.Comment }
  | { k: "go.slice"; elem: M.TypeRef }
  | { k: "go.map"; key: M.TypeRef; value: M.TypeRef }
  | { k: "go.results"; params: readonly M.Param[] }
  | { k: "go.trailing"; node: M.Node; text: string };

// Constructors of Go's own nodes.
export const G = {
  package: (name: string) => N.ext("go.package", { name }),
  imports: (specs: readonly M.Import[]) => N.ext("go.imports", { specs }),
  group: (keyword: "const" | "var", items: readonly M.Node[], doc?: M.Comment) =>
    N.ext("go.group", doc ? { keyword, items, doc } : { keyword, items }),
  slice: (elem: M.TypeRef) => N.ext("go.slice", { elem }),
  map: (key: M.TypeRef, value: M.TypeRef) => N.ext("go.map", { key, value }),
  results: (params: readonly M.Param[]) => N.ext("go.results", { params }),
  trailing: (node: M.Node, text: string) => N.ext("go.trailing", { node, text }),
};

export class GoPrinter extends Printer {
  override unit = "\t";
  /** A Const prints without its keyword inside a const ( … ) group. */
  private inGroup = false;

  override nullToken(): Doc {
    return "nil";
  }

  override comment(c: M.Comment): Doc {
    return this.marked("//", c.style === "line" ? c.lines : commentLines(c.lines));
  }

  override import(i: M.Import): Doc {
    return concat("import ", this.importSpec(i));
  }
  private importSpec(i: M.Import): Doc {
    return concat(i.alias ? concat(i.alias, " ") : empty, this.string(i.module));
  }

  private withDoc(doc: M.Comment | undefined, d: Doc): Doc {
    return doc ? lines([this.node(doc), d]) : d;
  }

  /** `type Name T` (a defined type). */
  override typeAlias(t: M.TypeAlias): Doc {
    return this.withDoc(t.doc, concat("type ", t.name, " ", this.node(t.type)));
  }

  override const(c: M.Const): Doc {
    const spec = concat(c.name, c.type ? concat(" ", this.node(c.type)) : empty, c.value ? concat(" = ", this.node(c.value)) : empty);
    return this.withDoc(c.doc, this.inGroup ? spec : concat("const ", spec));
  }

  override type(t: M.TypeDecl): Doc {
    if (t.kind !== "struct") return this.unsupported(t);
    const head = concat("type ", t.name, " struct");
    return this.withDoc(t.doc, t.members.length ? this.body(concat(head, " {"), t.members, "}") : concat(head, "{}"));
  }

  override field(f: M.Field): Doc {
    const tag = f.annotations.map((a) => structTag(a.name, (a.args[0] as M.Lit).value as string));
    return this.withDoc(f.doc, concat(f.name, " ", f.type ? this.node(f.type) : empty, tag.length ? concat(" ", join(" ", tag)) : empty));
  }

  override func(f: M.Func): Doc {
    const recv = f.receiver ? concat("(", this.param(f.receiver), ") ") : empty;
    const head = concat("func ", recv, f.name, "(", join(", ", f.params.map((p) => this.param(p))), ")", f.returns ? concat(" ", this.node(f.returns)) : empty);
    if (!f.body || !isStmts(f.body)) throw new Error(`go: func ${f.name} needs a statement body`);
    // An inline function: func … { return x }.
    if (f.mods.includes("inline")) return this.withDoc(f.doc, concat(head, " { ", join("; ", f.body.map((s) => this.node(s))), " }"));
    return this.withDoc(f.doc, this.body(concat(head, " {"), f.body, "}"));
  }

  override param(p: M.Param): Doc {
    const type = p.type ? this.node(p.type) : empty;
    if (p.name === "") return type;
    return concat(p.name, " ", p.mods.includes("variadic") ? "..." : empty, type);
  }

  override let(l: M.Let): Doc {
    return concat(join(", ", l.names), l.mods.includes("assign") ? " = " : " := ", l.value ? this.node(l.value) : empty);
  }

  override forEach(s: M.ForEach): Doc {
    return this.body(concat("for _, ", s.name, " := range ", this.node(s.iterable), " {"), s.body, "}");
  }

  override node(n: M.Node): Doc {
    if (n.k === "spread") return concat(this.node((n as M.Spread).expr), "...");
    return super.node(n);
  }

  /** A composite literal: Type{a, b} or Type{Name: v}. */
  private composite(type: M.TypeRef, items: readonly M.Expr[], layout: M.Layout): Doc {
    return concat(this.node(type), this.bracketed("{", items.map((i) => this.node(i)), "}", layout));
  }
  override new(n: M.New): Doc {
    return this.composite(n.type, n.args, n.layout);
  }
  override list(l: M.List): Doc {
    if (!l.type) throw new Error("go: a slice literal needs its type");
    return this.composite(l.type, l.items, l.layout);
  }

  override tref(t: M.TRef): Doc {
    return concat(t.nullable ? "*" : empty, t.name);
  }

  /** Unnamed results: (A, B). */
  override ttuple(t: M.TTuple): Doc {
    return concat("(", join(", ", t.items.map((i) => this.node(i))), ")");
  }

  override ext(n: M.Ext): Doc {
    const f = n as unknown as GoNode;
    switch (f.k) {
      case "go.package":
        return concat("package ", f.name);
      case "go.imports":
        return concat("import (", indent(concat(hardline, lines(f.specs.map((i) => this.importSpec(i))))), hardline, ")");
      case "go.group": {
        this.inGroup = true;
        const body = f.items.map((i) => this.node(i));
        this.inGroup = false;
        return this.withDoc(f.doc, concat(f.keyword, " (", indent(concat(hardline, lines(body))), hardline, ")"));
      }
      case "go.slice":
        return concat("[]", this.node(f.elem));
      case "go.map":
        return concat("map[", this.node(f.key), "]", this.node(f.value));
      case "go.results":
        return concat("(", join(", ", f.params.map((p) => this.param(p))), ")");
      case "go.trailing":
        return concat(this.node(f.node), " //", f.text);
      default:
        return super.ext(n);
    }
  }
}
