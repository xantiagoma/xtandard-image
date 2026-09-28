// Client-side decoders for the two placeholder hashes /info reports, so
// the Info panel can show what a page would paint before the image loads:
//
//   - BlurHash (https://blurha.sh, Wolt; the format is public and the
//     reference implementations are MIT): a base-83 string of DCT
//     components in linear RGB.
//   - ThumbHash (https://evanw.github.io/thumbhash, Evan Wallace, MIT):
//     bytes of LPQA DCT components; /info reports it as upper-case hex.
//
// Written for this page from the two format descriptions; both return
// {w, h, rgba} with rgba a Uint8ClampedArray ready for ImageData. Pure
// functions, no DOM except paint().

const B83 = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz#$%*+,-.:;=?@[]^_{|}~';

// base83 decodes s (throws on a character outside the alphabet).
function base83(s) {
  let v = 0;
  for (const c of s) {
    const d = B83.indexOf(c);
    if (d < 0) throw new Error(`blurhash: invalid character ${JSON.stringify(c)}`);
    v = v * 83 + d;
  }
  return v;
}

const toLinear = (c) => {
  const v = c / 255;
  return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
};

const toSRGB = (v) => {
  const x = Math.min(1, Math.max(0, v));
  return Math.round((x <= 0.0031308 ? x * 12.92 : 1.055 * x ** (1 / 2.4) - 0.055) * 255);
};

// blurhashSize returns the component counts {x, y} a hash declares.
export function blurhashSize(hash) {
  if (typeof hash !== 'string' || hash.length < 6) throw new Error('blurhash: too short');
  const flag = base83(hash[0]);
  return { x: (flag % 9) + 1, y: Math.floor(flag / 9) + 1 };
}

// decodeBlurhash paints hash at w×h. punch > 1 exaggerates the contrast,
// as the reference decoders allow.
export function decodeBlurhash(hash, w, h, punch = 1) {
  const { x: nx, y: ny } = blurhashSize(hash);
  if (hash.length !== 4 + 2 * nx * ny) {
    throw new Error(`blurhash: ${hash.length} characters, ${nx}×${ny} components need ${4 + 2 * nx * ny}`);
  }
  if (!(w > 0 && h > 0)) throw new Error('blurhash: empty size');
  const maxAC = ((base83(hash[1]) + 1) / 166) * punch;
  // colours[i] = [r, g, b] in linear light: the DC term, then the AC terms
  // (19 levels per channel, squared with sign).
  const colours = new Float64Array(nx * ny * 3);
  const dc = base83(hash.slice(2, 6));
  colours[0] = toLinear(dc >> 16);
  colours[1] = toLinear((dc >> 8) & 255);
  colours[2] = toLinear(dc & 255);
  const signSq = (q) => { const v = (q - 9) / 9; return Math.sign(v) * v * v * maxAC; };
  for (let i = 1; i < nx * ny; i++) {
    const v = base83(hash.slice(4 + i * 2, 6 + i * 2));
    colours[i * 3] = signSq(Math.floor(v / 361));
    colours[i * 3 + 1] = signSq(Math.floor(v / 19) % 19);
    colours[i * 3 + 2] = signSq(v % 19);
  }
  // Cosine tables: one row per component.
  const cx = new Float64Array(nx * w);
  const cy = new Float64Array(ny * h);
  for (let i = 0; i < nx; i++) for (let x = 0; x < w; x++) cx[i * w + x] = Math.cos((Math.PI * x * i) / w);
  for (let j = 0; j < ny; j++) for (let y = 0; y < h; y++) cy[j * h + y] = Math.cos((Math.PI * y * j) / h);
  const rgba = new Uint8ClampedArray(w * h * 4);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      let r = 0; let g = 0; let b = 0;
      for (let j = 0; j < ny; j++) {
        const fy = cy[j * h + y];
        for (let i = 0; i < nx; i++) {
          const f = cx[i * w + x] * fy;
          const k = (j * nx + i) * 3;
          r += colours[k] * f;
          g += colours[k + 1] * f;
          b += colours[k + 2] * f;
        }
      }
      const p = (y * w + x) * 4;
      rgba[p] = toSRGB(r);
      rgba[p + 1] = toSRGB(g);
      rgba[p + 2] = toSRGB(b);
      rgba[p + 3] = 255;
    }
  }
  return { w, h, rgba };
}

// hexBytes turns "A0180E…" into bytes.
export function hexBytes(hex) {
  const s = String(hex).trim();
  if (s.length % 2 || /[^0-9a-f]/i.test(s)) throw new Error('thumbhash: not hex');
  const out = new Uint8Array(s.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(s.slice(i * 2, i * 2 + 2), 16);
  return out;
}

// thumbhashHeader reads the fixed fields: DC terms, scales, the alpha and
// landscape flags and the luminance component counts.
function thumbhashHeader(b) {
  if (b.length < 5) throw new Error('thumbhash: too short');
  const h24 = b[0] | (b[1] << 8) | (b[2] << 16);
  const h16 = b[3] | (b[4] << 8);
  const alpha = (h24 >> 23) & 1;
  const landscape = (h16 >> 15) & 1;
  const n = h16 & 7; // the shorter side's component count
  const long = alpha ? 5 : 7;
  if (alpha && b.length < 6) throw new Error('thumbhash: too short');
  return {
    l: (h24 & 63) / 63,
    p: ((h24 >> 6) & 63) / 31.5 - 1,
    q: ((h24 >> 12) & 63) / 31.5 - 1,
    lScale: ((h24 >> 18) & 31) / 31,
    alpha,
    pScale: ((h16 >> 3) & 63) / 63,
    qScale: ((h16 >> 9) & 63) / 63,
    landscape,
    lx: Math.max(3, landscape ? long : n),
    ly: Math.max(3, landscape ? n : long),
    // The aspect ratio uses the counts before the minimum of 3.
    ratio: (landscape ? long : n) / (landscape ? n : long),
    a: alpha ? (b[5] & 15) / 15 : 1,
    aScale: alpha ? (b[5] >> 4) / 15 : 0,
  };
}

// thumbhashRatio is the approximate width / height the hash encodes.
export function thumbhashRatio(bytes) {
  return thumbhashHeader(bytes).ratio;
}

// decodeThumbhash renders the hash (bytes or hex) at most 32 px on the
// long side, the resolution it encodes.
export function decodeThumbhash(input) {
  const b = typeof input === 'string' ? hexBytes(input) : input;
  const hd = thumbhashHeader(b);
  // AC coefficients: 4-bit nibbles, low nibble first, after the header.
  let nib = 0;
  const start = hd.alpha ? 6 : 5;
  const channel = (nx, ny, scale) => {
    const ac = [];
    for (let cy = 0; cy < ny; cy++) {
      for (let cx = cy ? 0 : 1; cx * ny < nx * (ny - cy); cx++) {
        const byte = b[start + (nib >> 1)];
        if (byte === undefined) throw new Error('thumbhash: truncated');
        const v = (byte >> ((nib & 1) << 2)) & 15;
        nib++;
        ac.push((v / 7.5 - 1) * scale);
      }
    }
    return ac;
  };
  const lAC = channel(hd.lx, hd.ly, hd.lScale);
  // P and Q were quantised with a 1.25× saturation loss; boost it back.
  const pAC = channel(3, 3, hd.pScale * 1.25);
  const qAC = channel(3, 3, hd.qScale * 1.25);
  const aAC = hd.alpha ? channel(5, 5, hd.aScale) : null;

  const w = Math.round(hd.ratio > 1 ? 32 : 32 * hd.ratio);
  const h = Math.round(hd.ratio > 1 ? 32 / hd.ratio : 32);
  const rgba = new Uint8ClampedArray(w * h * 4);
  const nfx = Math.max(hd.lx, hd.alpha ? 5 : 3);
  const nfy = Math.max(hd.ly, hd.alpha ? 5 : 3);
  const fx = new Float64Array(nfx);
  const fy = new Float64Array(nfy);
  // sum adds a channel's triangle of AC terms (cx·ny < nx·(ny−cy)).
  const sum = (ac, nx, ny) => {
    let s = 0; let j = 0;
    for (let cy = 0; cy < ny; cy++) {
      const f2 = fy[cy] * 2;
      for (let cx = cy ? 0 : 1; cx * ny < nx * (ny - cy); cx++, j++) s += ac[j] * fx[cx] * f2;
    }
    return s;
  };
  for (let y = 0, i = 0; y < h; y++) {
    for (let cy = 0; cy < nfy; cy++) fy[cy] = Math.cos((Math.PI / h) * (y + 0.5) * cy);
    for (let x = 0; x < w; x++, i += 4) {
      for (let cx = 0; cx < nfx; cx++) fx[cx] = Math.cos((Math.PI / w) * (x + 0.5) * cx);
      const l = hd.l + sum(lAC, hd.lx, hd.ly);
      const p = hd.p + sum(pAC, 3, 3);
      const q = hd.q + sum(qAC, 3, 3);
      const a = hd.a + (aAC ? sum(aAC, 5, 5) : 0);
      // LPQ → RGB.
      const bl = l - (2 / 3) * p;
      const r = (3 * l - bl + q) / 2;
      const g = r - q;
      rgba[i] = 255 * Math.min(1, Math.max(0, r));
      rgba[i + 1] = 255 * Math.min(1, Math.max(0, g));
      rgba[i + 2] = 255 * Math.min(1, Math.max(0, bl));
      rgba[i + 3] = 255 * Math.min(1, Math.max(0, a));
    }
  }
  return { w, h, rgba };
}

// averageRGBA is the mean of a decoded image's pixels (alpha-weighted
// colour), for tests and the "average" readout.
export function averageRGBA({ rgba }) {
  let r = 0; let g = 0; let b = 0; let a = 0;
  for (let i = 0; i < rgba.length; i += 4) {
    const w = rgba[i + 3] / 255;
    r += rgba[i] * w; g += rgba[i + 1] * w; b += rgba[i + 2] * w; a += w;
  }
  const n = rgba.length / 4;
  return a ? { r: r / a, g: g / a, b: b / a, a: (a / n) * 255 } : { r: 0, g: 0, b: 0, a: 0 };
}

// paint draws a decoded image on canvas at its own resolution (CSS scales
// it up smoothly).
export function paint(canvas, img) {
  canvas.width = img.w;
  canvas.height = img.h;
  canvas.getContext('2d').putImageData(new ImageData(img.rgba, img.w, img.h), 0, 0);
}
