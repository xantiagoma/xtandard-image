// Go: github.com/xantiagoma/xtandard-image/clients/go. The keys are parsed
// first (they return errors), the builder gets a Config struct, URLs come
// from the fluent chains (img.Image(src).Resize(…).Build()) or a params
// struct (StreamURL), and every call that can fail is followed by an error
// check, inside func main.

import { concat, hardline, ifBreak, indent, join, line, softline, group } from '../doc.js';
import { lower, N, words } from '../model.js';
import { Printer } from '../printer.js';

const MODULE = 'github.com/xantiagoma/xtandard-image/clients/go';

// Go initialisms and fixed casings of the generated method names
// (clients/codegen/emitters/go.ts).
const CASING = {
  id: 'ID', url: 'URL', uri: 'URI', pdf: 'PDF', rgb: 'RGB', rgba: 'RGBA', hdr: 'HDR', sdr: 'SDR', dpi: 'DPI', dpr: 'DPR',
  fps: 'FPS', bps: 'BPS', crf: 'CRF', icc: 'ICC', exif: 'EXIF', iptc: 'IPTC', xmp: 'XMP', css: 'CSS', mime: 'MIME',
  http: 'HTTP', https: 'HTTPS', ftp: 'FTP', sftp: 'SFTP', json: 'JSON', md5: 'MD5', sha1: 'SHA1', sha256: 'SHA256',
  sha512: 'SHA512', dssim: 'DSSIM', ml: 'ML', jpeg: 'JPEG', jpg: 'JPG', png: 'PNG', gif: 'GIF', avif: 'AVIF', jxl: 'JXL',
  heic: 'HEIC', tiff: 'TIFF', bmp: 'BMP', ico: 'ICO', svg: 'SVG', webp: 'WebP', webm: 'WebM', mp4: 'MP4', mp3: 'MP3',
  m4a: 'M4A', wav: 'WAV', ogg: 'OGG', flac: 'FLAC', aac: 'AAC', pcm: 'PCM', hevc: 'HEVC', av1: 'AV1', vp9: 'VP9',
  h264: 'H264', h265: 'H265', hls: 'HLS', dash: 'DASH', m3u8: 'M3U8', mpd: 'MPD', ts: 'TS', fmp4: 'FMP4', cmaf: 'CMAF',
  mpegts: 'MPEGTS', s3: 'S3', gs: 'GS', abs: 'ABS', xi: 'Xi',
};
export const goIdent = (name) => words(name).map((w) => CASING[w.toLowerCase()] ?? w[0].toUpperCase() + w.slice(1)).join('');

const lit = (a) => N.lit(a);
const pkg = (p, name) => N.member(N.type(p), name, { static: true });
const chain = (head, links) => links.reduce((obj, [name, args]) => N.call(N.member(obj, name), args), head);
const opt = (o) => N.call(pkg('xtandard', 'Opt'), [N.str(o.name), ...o.args.map(lit)]);

const rules = {
  env: (e, ctx) => {
    ctx.uses.add('os');
    return N.call(pkg('os', 'Getenv'), [N.str(e.name)]);
  },
  // The keys are parsed before the builder (they can fail): the config
  // takes their address.
  'signing-key': (e, ctx) => {
    ctx.uses.add('xtandard');
    ctx.pre.push(N.let(['sk', 'err'], N.call(pkg('xtandard', 'ParseSigningKey'), [e.key, e.salt, pkg('xtandard', 'DefaultSignatureSize')]), { check: true }));
    return N.id('sk');
  },
  'encryption-key': (e, ctx) => {
    ctx.uses.add('xtandard');
    ctx.pre.push(N.let(['ek', 'err'], N.call(pkg('xtandard', 'ParseEncryptionKey'), [e.key, e.iv ?? N.str('')]), { check: true }));
    return N.id('ek');
  },
  client: (e, ctx) => {
    const p = e.kind === 'query' ? 'query' : 'imgproxy';
    ctx.uses.add(p);
    const c = e.config;
    const fields = [N.named('BaseURL', N.str(c.baseUrl))];
    if (c.mount) fields.push(N.named('Mount', N.str(c.mount)));
    if (c.signing) fields.push(N.named('Signing', N.ref(c.signing)));
    if (c.encryption) fields.push(N.named('Encryption', N.ref(c.encryption)));
    if (c.sourceEncoding) {
      ctx.uses.add('xtandard');
      fields.push(N.named('SourceEncoding', pkg('xtandard', `SourceEncoding${c.sourceEncoding === 'plain' ? 'Plain' : 'Encrypted'}`)));
    }
    if (c.separator) fields.push(N.named('Separator', N.str(c.separator)));
    ctx.pkg = p;
    return N.call(pkg(p, 'New'), [N.composite(`${p}.Config`, fields)]);
  },
  request: (e, ctx) => {
    const d = e.data;
    const v = N.id(e.client);
    const links = [];
    const options = (p) => p.forEach((o) => links.push([goIdent(o.name), o.args.map(lit)]));
    switch (e.op) {
      case 'streamUrl': {
        ctx.uses.add('xtandard');
        const f = [N.named('Protocol', pkg('xtandard', `StreamProtocol${d.protocol === 'hls' ? 'HLS' : 'DASH'}`))];
        if (d.renditions.length) f.push(N.named('Renditions', N.composite('[]int', d.renditions.map(N.int))));
        if (d.segmentDuration) f.push(N.named('SegmentDuration', N.int(d.segmentDuration)));
        if (d.segmentFormat) f.push(N.named('SegmentFormat', pkg('xtandard', `SegmentFormat${d.segmentFormat === 'ts' ? 'TS' : 'FMP4'}`)));
        if (d.pipelines[0].length) f.push(N.named('Options', N.composite('[]xtandard.VideoOption', d.pipelines[0].map(opt))));
        return N.call(N.member(v, 'StreamURL'), [N.str(d.source), N.composite(`${ctx.pkg}.StreamParams`, f)]);
      }
      case 'imaginaryUrl':
        ctx.uses.add('xtandard');
        return N.call(N.member(v, 'ImaginaryURL'), [
          N.call(pkg('xtandard', 'ImaginaryEndpoint'), [N.str(d.endpoint)]), N.str(d.source),
          ...d.params.map(([k, a]) => N.call(pkg('query', 'P'), [N.str(k), lit(a)])),
        ]);
      case 'infoUrl': options(d.pipelines[0]); return chain(N.call(N.member(v, 'Info'), [N.str(d.source)]), [...links, ...(d.pdfPassword ? [['WithPDFPassword', [d.pdfPassword]]] : []), ['Build', []]]);
      case 'videoUrl': options(d.pipelines[0]); return chain(N.call(N.member(v, 'Video'), [N.str(d.source)]), [...links, ...(d.extension ? [['Extension', [N.str(d.extension)]]] : []), ['Build', []]]);
      default: {
        for (const [k, a] of d.params) links.push(['Param', [N.str(k), lit(a)]]);
        d.pipelines.forEach((p, i) => {
          if (i) links.push(['Pipeline', []]);
          options(p);
        });
        if (d.extension) links.push(['Extension', [N.str(d.extension)]]);
        if (d.pdfPassword) links.push(['WithPDFPassword', [d.pdfPassword]]);
        return chain(N.call(N.member(v, 'Image'), [N.str(d.source)]), [...links, ['Build', []]]);
      }
    }
  },
  'sign-xi-url': (e, ctx) => {
    ctx.uses.add('xtandard');
    return N.call(pkg('xtandard', 'SignXiURL'), [N.str(e.url), e.key, N.str(e.mount)]);
  },
  print: (e, ctx) => (ctx.uses.add('fmt'), N.stmt(N.call(pkg('fmt', 'Println'), [e.expr]))),
};

const errCheck = () => N.if(N.binary('!=', N.id('err'), N.type('nil')), [N.stmt(N.call(pkg('log', 'Fatal'), [N.id('err')]))]);

export function refine(program) {
  const ctx = { uses: new Set(['log']), pre: [], pkg: 'imgproxy' };
  const body = [];
  for (const s of program.body) {
    const out = lower(s, rules, ctx);
    body.push(...ctx.pre);
    ctx.pre = [];
    // Every value a client call returns comes with an error.
    body.push(out.k === 'let' && !out.names.includes('err') ? { ...out, names: [...out.names, 'err'], check: true } : out);
  }
  const checked = body.flatMap((s) => (s.check ? [s, errCheck()] : [s]));
  const std = ['fmt', 'log', 'os'].filter((p) => ctx.uses.has(p)).map((p) => N.imp(p, [], { std: true }));
  const mod = [...(ctx.uses.has('xtandard') ? [N.imp(MODULE, [], { alias: 'xtandard' })] : []), ...['imgproxy', 'query'].filter((p) => ctx.uses.has(p)).map((p) => N.imp(`${MODULE}/${p}`))];
  return N.program([N.func('main', checked)], { header: [N.cmd('package main', [])], imports: [...std, ...mod] });
}

export class GoPrinter extends Printer {
  width = 100;
  unit = '\t';
  chainDotAtEnd = true;
  nullToken() { return 'nil'; }
  imports(list) {
    const lines = (l) => l.map((i) => concat('\t', i.alias ? `${i.alias} ` : '', this.string(i.module)));
    const std = lines(list.filter((i) => i.std));
    const mod = lines(list.filter((i) => !i.std));
    return concat('import (', hardline, join(concat(hardline, hardline), [join(hardline, std), join(hardline, mod)].filter((_, i) => (i ? mod.length : std.length))), hardline, ')');
  }
  letStmt(s) { return concat(join(', ', s.names), ' := ', this.expr(s.value)); }
  funcHead(f) { return concat('func ', f.name, '()'); }
  // gofmt aligns the values of a broken composite literal's fields.
  x_composite(e) {
    const named = e.items.filter((i) => i.k === 'named');
    const pad = Math.max(0, ...named.map((i) => i.name.length));
    const items = e.items.map((i) => (i.k === 'named'
      ? concat(i.name, ':', ifBreak(' '.repeat(pad - i.name.length + 1), ' '), this.expr(i.value))
      : this.expr(i)));
    if (!items.length) return concat(e.type, '{}');
    return group(concat(e.type, '{', indent(concat(softline, join(concat(',', line), items))), ifBreak(','), softline, '}'));
  }
}

export const go = { id: 'go', label: 'Go', refine, printer: new GoPrinter() };
