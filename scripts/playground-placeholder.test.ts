// Unit tests of the playground's BlurHash and ThumbHash decoders
// (internal/playground/assets/placeholder.js, WP-109) against the encoder
// test vectors of internal/imageinfo: each reference hash is decoded and
// compared perceptually with its source PNG: mean colour, and the
// correlation of the coarse pattern (a transposed, mirrored or mis-signed
// component fails). Run with `bun test scripts/playground-placeholder.test.ts`;
// scripts/playground-check.ts runs the same decoders in the browser.
import { describe, expect, test } from "bun:test";
import { inflateSync } from "node:zlib";
// @ts-expect-error: plain ES module without types
import * as ph from "../internal/playground/assets/placeholder.js";

type Img = { w: number; h: number; rgba: Uint8ClampedArray | Uint8Array };

const root = new URL("../internal/imageinfo/testdata/", import.meta.url);

// readPNG decodes an 8-bit, non-interlaced RGB or RGBA PNG (the vectors).
async function readPNG(path: string): Promise<Img> {
  const buf = new Uint8Array(await Bun.file(new URL(path, root)).arrayBuffer());
  const dv = new DataView(buf.buffer);
  let off = 8;
  let w = 0, h = 0, type = 0;
  const idat: Uint8Array[] = [];
  while (off < buf.length) {
    const len = dv.getUint32(off);
    const name = new TextDecoder().decode(buf.subarray(off + 4, off + 8));
    const data = buf.subarray(off + 8, off + 8 + len);
    if (name === "IHDR") {
      w = dv.getUint32(off + 8);
      h = dv.getUint32(off + 12);
      if (data[8] !== 8 || data[12] !== 0) throw new Error(`${path}: unsupported PNG`);
      type = data[9];
    } else if (name === "IDAT") idat.push(data);
    off += 12 + len;
  }
  const ch = type === 6 ? 4 : type === 2 ? 3 : 0;
  if (!ch) throw new Error(`${path}: colour type ${type}`);
  const raw = inflateSync(Buffer.concat(idat));
  const stride = w * ch;
  const px = new Uint8Array(h * stride);
  for (let y = 0; y < h; y++) {
    const f = raw[y * (stride + 1)];
    for (let x = 0; x < stride; x++) {
      const v = raw[y * (stride + 1) + 1 + x];
      const a = x >= ch ? px[y * stride + x - ch] : 0;
      const b = y ? px[(y - 1) * stride + x] : 0;
      const c = x >= ch && y ? px[(y - 1) * stride + x - ch] : 0;
      let p = 0;
      if (f === 1) p = a;
      else if (f === 2) p = b;
      else if (f === 3) p = (a + b) >> 1;
      else if (f === 4) {
        const e = a + b - c, pa = Math.abs(e - a), pb = Math.abs(e - b), pc = Math.abs(e - c);
        p = pa <= pb && pa <= pc ? a : pb <= pc ? b : c;
      }
      px[y * stride + x] = (v + p) & 255;
    }
  }
  const rgba = new Uint8Array(w * h * 4);
  for (let i = 0; i < w * h; i++) {
    rgba.set(px.subarray(i * ch, i * ch + 3), i * 4);
    rgba[i * 4 + 3] = ch === 4 ? px[i * ch + 3] : 255;
  }
  return { w, h, rgba };
}

// resize box-averages img to w×h.
function resize(img: Img, w: number, h: number): Img {
  const out = new Uint8ClampedArray(w * h * 4);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const x0 = Math.floor((x * img.w) / w), x1 = Math.max(x0 + 1, Math.floor(((x + 1) * img.w) / w));
      const y0 = Math.floor((y * img.h) / h), y1 = Math.max(y0 + 1, Math.floor(((y + 1) * img.h) / h));
      const s = [0, 0, 0, 0];
      for (let yy = y0; yy < y1; yy++) for (let xx = x0; xx < x1; xx++) for (let c = 0; c < 4; c++) s[c] += img.rgba[(yy * img.w + xx) * 4 + c];
      const n = (x1 - x0) * (y1 - y0);
      for (let c = 0; c < 4; c++) out[(y * w + x) * 4 + c] = s[c] / n;
    }
  }
  return { w, h, rgba: out };
}

// correlation is the Pearson correlation of the two images' channel c
// (c = 3: alpha; otherwise luma): a transposed, mirrored or mis-signed
// component decodes a different pattern and scores near or below 0.
function correlation(a: Img, b: Img, c = -1) {
  const val = (img: Img, i: number) =>
    c >= 0 ? img.rgba[i + c] : 0.299 * img.rgba[i] + 0.587 * img.rgba[i + 1] + 0.114 * img.rgba[i + 2];
  const n = a.rgba.length / 4;
  let sa = 0, sb = 0;
  for (let i = 0; i < a.rgba.length; i += 4) { sa += val(a, i); sb += val(b, i); }
  const [ma, mb] = [sa / n, sb / n];
  let cov = 0, va = 0, vb = 0;
  for (let i = 0; i < a.rgba.length; i += 4) {
    const [x, y] = [val(a, i) - ma, val(b, i) - mb];
    cov += x * y; va += x * x; vb += y * y;
  }
  return va && vb ? cov / Math.sqrt(va * vb) : 1;
}

// flatLuma reports a luma spread under 2 levels.
function flatLuma(img: Img) {
  let lo = 255, hi = 0;
  for (let i = 0; i < img.rgba.length; i += 4) {
    const y = 0.299 * img.rgba[i] + 0.587 * img.rgba[i + 1] + 0.114 * img.rgba[i + 2];
    lo = Math.min(lo, y); hi = Math.max(hi, y);
  }
  return hi - lo < 2;
}

// linearMean is the mean colour in linear light (BlurHash's DC term).
function linearMean(img: Img) {
  const lin = (c: number) => { const v = c / 255; return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; };
  const s = [0, 0, 0];
  for (let i = 0; i < img.rgba.length; i += 4) for (let c = 0; c < 3; c++) s[c] += lin(img.rgba[i + c]);
  return s.map((v) => (v / (img.rgba.length / 4)) * 255);
}

// coarse is img on the grid a hash with that many components can
// represent: the comparison ignores detail no placeholder carries.
const coarse = (img: Img, nx: number, ny: number) => resize(img, Math.max(2, nx * 2), Math.max(2, ny * 2));

const vectors = async (kind: string) => (await Bun.file(new URL(`${kind}/vectors.json`, root)).json()).vectors;

describe("blurhash", async () => {
  for (const v of await vectors("blurhash")) {
    test(`${v.image} ${v.x}×${v.y}`, async () => {
      expect(ph.blurhashSize(v.hash)).toEqual({ x: v.x, y: v.y });
      const ref = await readPNG(`blurhash/${v.image}`);
      // Decode at a small size (the placeholder's use) with the image's aspect.
      const w = 64, h = Math.round((64 * ref.h) / ref.w);
      const got = ph.decodeBlurhash(v.hash, w, h);
      expect(got.rgba.length).toBe(w * h * 4);
      const [gm, rm] = [linearMean(got), linearMean(ref)];
      for (let c = 0; c < 3; c++) expect(Math.abs(gm[c] - rm[c])).toBeLessThan(6);
      const [g, r] = [coarse(got, v.x, v.y), coarse(ref, v.x, v.y)];
      // A flat source has no pattern to match (pic4); a hash whose AC
      // maximum (second character) is small carries a faint one (pic1 2×8).
      const faint = "0123456789".includes(v.hash[1]);
      if (v.x * v.y > 1 && !flatLuma(r)) expect(correlation(g, r)).toBeGreaterThan(faint ? 0.2 : 0.5);
    });
  }
  test("rejects malformed hashes", () => {
    expect(() => ph.decodeBlurhash("LEHV6nWB2yk8pyo0adR*.7kCMdn", 8, 8)).toThrow();
    expect(() => ph.decodeBlurhash("LEHV6nWB2yk8pyo0adR*.7kCMd\"j", 8, 8)).toThrow();
    expect(() => ph.decodeBlurhash("", 8, 8)).toThrow();
  });
});

describe("thumbhash", async () => {
  for (const v of await vectors("thumbhash")) {
    test(v.image, async () => {
      const ref = await readPNG(`thumbhash/${v.image}`);
      const got = ph.decodeThumbhash(v.hash);
      expect(Math.max(got.w, got.h)).toBe(32);
      // The encoded aspect ratio is approximate (component counts).
      expect(Math.abs(Math.log(got.w / got.h) - Math.log(ref.w / ref.h))).toBeLessThan(0.35);
      const alpha = v.image === "alpha.png";
      const [g, r] = [coarse(got, 4, 4), coarse(ref, 4, 4)];
      const [gm, rm] = [ph.averageRGBA(g), ph.averageRGBA(r)];
      for (const c of ["r", "g", "b"] as const) expect(Math.abs(gm[c] - rm[c])).toBeLessThan(alpha ? 24 : 12);
      expect(Math.abs(gm.a - rm.a)).toBeLessThan(12);
      expect(correlation(g, r)).toBeGreaterThan(0.5);
      if (alpha) expect(correlation(g, r, 3)).toBeGreaterThan(0.8);
    });
  }
  test("rejects malformed hashes", () => {
    expect(() => ph.decodeThumbhash("A0180")).toThrow();
    expect(() => ph.decodeThumbhash("zz")).toThrow();
    expect(() => ph.decodeThumbhash("A0180E0C")).toThrow();
  });
});
