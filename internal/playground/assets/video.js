// Video mode (WP-127): when the source is a video or an audio file, the
// session can build a video transformation (/video on imgproxy mounts,
// /q/video on query mounts, URL-DIALECTS §3.8) instead of an image of it.
// This panel holds the typed controls a person reaches for first: a trim
// timeline with start and end handles over the source's duration, the
// output container and codecs this server's build writes, audio (mute,
// volume, loudness, fades, sample rate, channels), overlays (a logo with a
// window and fades, a timecode) and waveform pictures. Every control
// writes the video catalogue's own options into the first pipeline, so the
// builder below shows them, the URL updates live and the session shares
// like any other.

import { $, el, toast } from './dom.js';
import { WM_FIXTURE } from './builder.js';

// AUDIO_EXT are file extensions of audio sources, a fallback when the
// server cannot probe.
const AUDIO_EXT = /\.(mp3|m4a|aac|wav|flac|ogg|oga|opus)(\?|$)/i;
const VIDEO_EXT = /\.(mp4|m4v|mov|webm|mkv|avi|ogv|3gp|ts)(\?|$)/i;

// CONTAINERS describes each output: its MIME type (for canPlayType), the
// default codecs per build and whether it holds video.
const CONTAINERS = {
  mp4: { mime: 'video/mp4', video: true, audio: 'aac', label: 'MP4' },
  webm: { mime: 'video/webm', video: true, audio: 'opus', vcodec: 'vp9', label: 'WebM' },
  m4a: { mime: 'audio/mp4', audio: 'aac', label: 'M4A (audio)' },
  mp3: { mime: 'audio/mpeg', audio: 'mp3', label: 'MP3 (audio)' },
  opus: { mime: 'audio/ogg; codecs=opus', audio: 'opus', label: 'Opus (audio)' },
  ogg: { mime: 'audio/ogg; codecs=opus', audio: 'opus', label: 'Ogg Opus (audio)' },
  flac: { mime: 'audio/flac', audio: 'flac', label: 'FLAC (audio)' },
  wav: { mime: 'audio/wav', audio: 'pcm', label: 'WAV (audio)' },
  png: { mime: 'image/png', image: true, label: 'PNG waveform' },
  webp: { mime: 'image/webp', image: true, label: 'WebP waveform' },
};

// CODEC_TYPES are canPlayType strings of the result codecs.
const CODEC_TYPES = {
  h264: 'video/mp4; codecs="avc1.42E01E, mp4a.40.2"',
  av1: 'video/mp4; codecs="av01.0.05M.08, mp4a.40.2"',
  hevc: 'video/mp4; codecs="hvc1.1.6.L93.B0, mp4a.40.2"',
  vp9: 'video/webm; codecs="vp9, opus"',
};

const VIDEO_CODECS = { mp4: ['h264', 'av1', 'hevc'], webm: ['vp9', 'av1'] };
const AUDIO_CODECS = { mp4: ['aac'], m4a: ['aac'], webm: ['opus'], opus: ['opus'], ogg: ['opus'], mp3: ['mp3'], flac: ['flac'], wav: ['pcm'] };
const SAMPLE_RATES = [8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000, 88200, 96000];

// mediaKind classifies a source from the server's probe (api/probe),
// its /info, or its extension: 'video', 'audio' or null.
export function mediaKind(probe, info, source) {
  if (probe && !probe.error) return probe.video ? 'video' : probe.audio ? 'audio' : null;
  if (info?.video_meta?.duration > 0) return 'video';
  const path = String(source ?? '').split('#')[0];
  if (AUDIO_EXT.test(path)) return 'audio';
  if (VIDEO_EXT.test(path)) return 'video';
  return null;
}

// canPlay says whether this browser plays a container (and the codec this
// server writes into it).
export function canPlay(container, vcodec) {
  const c = CONTAINERS[container];
  if (!c || c.image) return true;
  const probe = document.createElement(c.video ? 'video' : 'audio');
  const type = c.video && CODEC_TYPES[vcodec] ? CODEC_TYPES[vcodec] : c.mime;
  return probe.canPlayType(type) !== '';
}

const fmtSec = (s) => `${(Math.round(s * 100) / 100).toFixed(2)} s`;
const round = (s) => String(Math.round(s * 1000) / 1000);

export class VideoPanel {
  // meta is api/meta; state the session; grammar() the current mount's
  // grammar (null in raw mode); afterEdit() redraws the builder;
  // onChange(now) re-renders; onMode(on) switches the session's mode.
  constructor({ meta, state, grammar, afterEdit, onChange, onMode }) {
    this.meta = meta;
    this.caps = meta.video_transform ?? null;
    this.state = state;
    this.grammar = grammar;
    this.afterEdit = afterEdit;
    this.onChange = onChange;
    this.onMode = onMode;
    this.source = '';
    this.probe = null; // api/probe of the source (MediaInfo), or {error}
    this.info = null; // the source's /info
    this.timer = 0;
    $('video-mode-image').addEventListener('change', () => this.onMode(false));
    $('video-mode-video').addEventListener('change', () => this.onMode(true));
  }

  // ---- what the source is ------------------------------------------------

  // setSource resets the source's facts when it changed.
  setSource(src) {
    if (src === this.source) return;
    this.source = src;
    this.probe = null;
    this.info = null;
    this.render();
  }

  setProbe(src, probe) {
    if (src !== this.source) return;
    this.probe = probe;
    this.render();
  }

  setInfo(src, info) {
    if (src !== this.source) return;
    this.info = info && !info.error ? info : null;
    this.render();
  }

  kind() { return mediaKind(this.probe, this.info, this.source); }

  duration() {
    const d = this.probe?.duration || this.info?.video_meta?.duration || 0;
    return d > 0 ? d : 0;
  }

  hasAudio() {
    if (this.probe && !this.probe.error) return Boolean(this.probe.audio);
    const streams = this.info?.video_streams;
    if (Array.isArray(streams)) return streams.some((s) => s.type === 'audio');
    return this.kind() === 'audio';
  }

  // capable reports whether the current mount builds video URLs.
  capable() { return ['imgproxy', 'query'].includes(this.grammar()); }

  // ---- the first pipeline's entries ---------------------------------------

  pipeline() { return this.state.pipelines[0] ?? (this.state.pipelines[0] = []); }

  entry(name) { return this.pipeline().find((x) => x.name === name); }

  get(name, i = 0) {
    const v = this.entry(name)?.values?.[i];
    return v === undefined || v === '' ? null : String(v);
  }

  // set writes values for name (null removes the entry).
  set(name, values) {
    const p = this.pipeline();
    const i = p.findIndex((x) => x.name === name);
    if (values === null) {
      if (i >= 0) p.splice(i, 1);
      return;
    }
    const e = { name, form: 0, groups: 1, values: values.map(String), on: true };
    if (i >= 0) p[i] = e;
    else p.push(e);
  }

  // edited redraws and renders: at once (a committed change), or 250 ms
  // after the last of a run of edits (dragging). A commit replaces the
  // pending one, so input + change render once.
  edited(now = false) {
    this.afterEdit();
    this.sync();
    clearTimeout(this.timer);
    if (now) this.onChange(true);
    else this.timer = setTimeout(() => this.onChange(true), 250);
  }

  // container is the output container: the format option, else the
  // imgproxy extension, else mp4.
  container() {
    return this.get('format') ?? (this.grammar() === 'imgproxy' && this.state.ext ? this.state.ext : 'mp4');
  }

  setContainer(c) {
    if (this.grammar() === 'imgproxy') {
      this.set('format', null);
      this.state.ext = c === 'mp4' ? '' : c;
    } else {
      this.set('format', c === 'mp4' ? null : [c]);
    }
    // Codecs the new container cannot hold go.
    const vc = this.get('video_codec');
    if (vc && !(VIDEO_CODECS[c] ?? []).includes(vc)) this.set('video_codec', null);
    const ac = this.get('audio_codec');
    if (ac && !(AUDIO_CODECS[c] ?? []).includes(ac)) this.set('audio_codec', null);
    if (CONTAINERS[c]?.image) {
      if (!this.entry('waveform')) this.set('waveform', ['1', '2266cc', '', '0']);
    } else if (this.entry('waveform')) {
      this.set('waveform', null);
    }
    if (!CONTAINERS[c]?.video) this.set('mute', null);
  }

  // The cut in source seconds: [start, end]; end is the duration (or null
  // when unknown) without an end or duration option.
  cut() {
    const start = Number(this.get('start') ?? 0) || 0;
    const end = this.get('end');
    const dur = this.get('duration');
    if (end !== null) return [start, Number(end)];
    if (dur !== null) return [start, start + Number(dur)];
    return [start, this.duration() || null];
  }

  setCut(start, end) {
    const d = this.duration();
    this.set('start', start > 0 ? [round(start)] : null);
    this.set('duration', null);
    this.set('end', end !== null && (!d || end < d - 0.0005) ? [round(end)] : null);
  }

  // ---- UI ------------------------------------------------------------------

  // mp4Codec is the video codec of mp4 on this server.
  mp4Codec() { return this.caps?.mp4_video || 'h264'; }

  containerLabel(c) {
    const info = CONTAINERS[c];
    let codecs = '';
    if (c === 'mp4') codecs = `${this.mp4Codec() === 'h264' ? 'H.264' : 'AV1'} + AAC`;
    else if (c === 'webm') codecs = 'VP9 + Opus';
    else if (!info.image) codecs = info.audio.toUpperCase();
    const plays = info.image ? '' : canPlay(c, c === 'mp4' ? this.mp4Codec() : 'vp9') ? '' : ' · not in this browser';
    return `${c} — ${info.label}${codecs ? `, ${codecs}` : ''}${plays}`;
  }

  render() {
    const box = $('video-panel');
    const kind = this.kind();
    box.hidden = !kind;
    if (!kind) return;
    const on = Boolean(this.state.video);
    const mi = $('video-mode-image');
    const mv = $('video-mode-video');
    mi.checked = !on;
    mv.checked = on;
    mi.disabled = kind === 'audio';
    $('video-mode-image-label').title = kind === 'audio' ? 'An audio file has no frames: use the video transform (audio output or a waveform).' : '';
    const why = !this.caps ? 'Video transformations are off on this server (XI_VIDEO_TRANSFORM_ENABLED=true on the full or gpl image).'
      : !this.capable() ? 'Built on an imgproxy or query mount (/video, /q/video); switching to it.' : '';
    mv.disabled = !this.caps;
    $('video-mode-video-label').title = why;
    const facts = $('video-facts');
    const d = this.duration();
    const p = this.probe && !this.probe.error ? this.probe : null;
    facts.textContent = [
      kind === 'audio' ? 'Audio file' : 'Video',
      d ? fmtSec(d) : 'duration unknown',
      p?.video ? `${p.video.width}×${p.video.height} ${p.video.codec}${p.video.fps ? ` ${p.video.fps} fps` : ''}` : '',
      p?.audio ? `${p.audio.codec} ${p.audio.sample_rate ? `${p.audio.sample_rate / 1000} kHz` : ''} ${p.audio.channels === 1 ? 'mono' : p.audio.channels === 2 ? 'stereo' : p.audio.channels ? `${p.audio.channels} ch` : ''}`.trim() : (p ? 'no audio' : ''),
    ].filter(Boolean).join(' · ');
    const body = $('video-body');
    body.hidden = !on || !this.caps;
    $('video-caps').replaceChildren(...this.capsNodes());
    if (body.hidden) return;
    this.renderBody(body);
  }

  // capsNodes says what this server's build writes.
  capsNodes() {
    const c = this.caps;
    if (!c) return [el('span', { class: 'hint', text: 'This server does not run video transformations.' })];
    const has = (x) => c.codecs.includes(x);
    const parts = [
      `mp4 = ${c.mp4_video === 'h264' ? 'H.264' : 'AV1'} + AAC (${has('h264') ? 'gpl image: H.264 and HEVC encoders' : 'full image: no H.264 encoder, AV1 instead'})`,
      `webm = VP9 + Opus${has('av1') ? ', AV1 on request' : ''}`,
      `audio: ${['aac', 'opus', 'mp3', 'flac', 'pcm'].filter(has).join(', ')}`,
    ];
    const lim = [c.max_output_duration ? `≤ ${c.max_output_duration} s` : '', c.max_output_pixels ? `≤ ${(c.max_output_pixels / 1e6).toFixed(1)} MP` : '', c.max_fps ? `≤ ${c.max_fps} fps` : ''].filter(Boolean);
    if (lim.length) parts.push(`limits ${lim.join(', ')}`);
    if (c.unsigned_policy === 'deny') parts.push('unsigned video URLs are refused (signed only)');
    return [el('span', { class: 'hint', text: `This server: ${parts.join(' · ')}.` })];
  }

  renderBody(body) {
    body.replaceChildren(this.trimNodes(), this.outputNodes(), this.audioNodes(), this.overlayNodes());
    this.sync();
  }

  // trimNodes: the timeline with two handles, and number inputs.
  trimNodes() {
    const d = this.duration();
    const step = 0.01;
    const max = d || 3600;
    const start = el('input', { type: 'range', id: 'trim-start', class: 'tl-start', min: 0, max, step, value: 0, 'aria-label': 'Trim start (seconds)' });
    const end = el('input', { type: 'range', id: 'trim-end', class: 'tl-end', min: 0, max, step, value: max, 'aria-label': 'Trim end (seconds)' });
    const startN = el('input', { type: 'number', id: 'trim-start-n', min: 0, max: d || undefined, step: 0.01, class: 'num-short', 'aria-label': 'Start (seconds)' });
    const endN = el('input', { type: 'number', id: 'trim-end-n', min: 0, max: d || undefined, step: 0.01, class: 'num-short', 'aria-label': 'End (seconds)' });
    const out = el('output', { id: 'trim-readout', for: 'trim-start trim-end' });
    const whole = el('button', { type: 'button', class: 'small', id: 'trim-whole', text: 'Whole clip' });
    const minGap = 0.1;
    const fromRanges = (moved, now) => {
      let a = Number(start.value);
      let b = Number(end.value);
      if (b - a < minGap) {
        if (moved === 'start') a = Math.max(0, b - minGap);
        else b = Math.min(max, a + minGap);
        start.value = String(a);
        end.value = String(b);
      }
      this.setCut(a, d ? b : (b >= max ? null : b));
      this.edited(now);
    };
    start.addEventListener('input', () => fromRanges('start', false));
    end.addEventListener('input', () => fromRanges('end', false));
    start.addEventListener('change', () => fromRanges('start', true));
    end.addEventListener('change', () => fromRanges('end', true));
    const fromNumbers = () => {
      const a = Math.max(0, Number(startN.value) || 0);
      const b = endN.value === '' ? null : Number(endN.value);
      if (b !== null && b - a < minGap) return;
      this.setCut(a, b);
      this.edited(true);
    };
    startN.addEventListener('change', fromNumbers);
    endN.addEventListener('change', fromNumbers);
    whole.addEventListener('click', () => { this.setCut(0, null); this.edited(true); });
    const timeline = el('div', { class: 'timeline', id: 'timeline', role: 'group', 'aria-label': 'Trim the source' },
      el('div', { class: 'tl-track', 'aria-hidden': 'true' }, el('div', { class: 'tl-sel' })), start, end);
    if (!d) timeline.classList.add('unknown');
    return el('fieldset', { class: 'vgroup' }, el('legend', { text: 'Trim' }),
      timeline,
      el('div', { class: 'row wrap' },
        el('label', { class: 'inline', for: 'trim-start-n', text: 'Start' }), startN,
        el('label', { class: 'inline', for: 'trim-end-n', text: 'End' }), endN,
        out, whole),
      el('p', { class: 'hint', text: d ? `Drag the handles over the ${fmtSec(d)} of the source: start and end (seconds) are exact cuts from the keyframe before them.` : 'The source duration is unknown: type the start and end.' }));
  }

  // outputNodes: container, codecs and quality.
  outputNodes() {
    const c = this.caps;
    const containers = c.containers.filter((x) => CONTAINERS[x]);
    const sel = el('select', { id: 'video-container' }, ...containers.map((x) => el('option', { value: x, text: this.containerLabel(x) })));
    const kind = this.kind();
    for (const o of sel.options) if (CONTAINERS[o.value].video && kind === 'audio') o.disabled = true;
    sel.addEventListener('change', () => { this.setContainer(sel.value); this.edited(true); });
    const vc = el('select', { id: 'video-vcodec' }, el('option', { value: '', text: 'auto' }),
      ...['h264', 'av1', 'vp9', 'hevc'].map((x) => el('option', { value: x, text: x })));
    vc.addEventListener('change', () => { this.set('video_codec', vc.value ? [vc.value] : null); this.edited(true); });
    const ac = el('select', { id: 'video-acodec' }, el('option', { value: '', text: 'auto' }),
      ...['aac', 'opus', 'mp3', 'flac', 'pcm'].map((x) => el('option', { value: x, text: x })));
    ac.addEventListener('change', () => { this.set('audio_codec', ac.value ? [ac.value] : null); this.edited(true); });
    const q = el('input', { type: 'number', id: 'video-quality', min: 1, max: 100, step: 1, class: 'num-short', placeholder: 'auto' });
    q.addEventListener('change', () => { this.set('quality', q.value ? [q.value] : null); this.edited(true); });
    const w = el('input', { type: 'number', id: 'video-width', min: 0, max: 8192, step: 2, class: 'num-short', placeholder: 'auto' });
    const h = el('input', { type: 'number', id: 'video-height', min: 0, max: 8192, step: 2, class: 'num-short', placeholder: 'auto' });
    const fit = el('select', { id: 'video-fit' }, ...[['fit', 'fit (scale down)'], ['fill', 'fill (cover, crop)'], ['fill-down', 'fill-down'], ['force', 'force (stretch)']]
      .map(([v, t]) => el('option', { value: v, text: t })));
    const size = () => {
      const ww = Number(w.value) || 0;
      const hh = Number(h.value) || 0;
      this.set('width', null);
      this.set('height', null);
      this.set('size', null);
      this.set('resize', ww || hh ? [fit.value, String(ww), String(hh)] : null);
      this.edited(true);
    };
    for (const x of [w, h]) x.addEventListener('change', size);
    fit.addEventListener('change', size);
    const fps = el('input', { type: 'number', id: 'video-fps', min: 1, max: this.caps.max_fps || 240, step: 'any', class: 'num-short', placeholder: 'source' });
    fps.addEventListener('change', () => { this.set('fps', fps.value ? [fps.value] : null); this.edited(true); });
    const play = el('p', { class: 'hint', id: 'video-plays' });
    return el('fieldset', { class: 'vgroup' }, el('legend', { text: 'Output' }),
      el('label', { for: 'video-container', text: 'Container' }), sel, play,
      el('div', { class: 'vgrid' },
        el('div', {}, el('label', { for: 'video-vcodec', text: 'Video codec' }), vc),
        el('div', {}, el('label', { for: 'video-acodec', text: 'Audio codec' }), ac),
        el('div', {}, el('label', { for: 'video-quality', text: 'Quality (1–100)' }), q),
        el('div', {}, el('label', { for: 'video-fps', text: 'Frame rate' }), fps),
        el('div', {}, el('label', { for: 'video-width', text: 'Width' }), w),
        el('div', {}, el('label', { for: 'video-height', text: 'Height' }), h),
        el('div', {}, el('label', { for: 'video-fit', text: 'Resize' }), fit)),
      el('p', { class: 'hint', text: 'Crop, gravity, rotation, flips, CRF, bit-rate cap and keyframe interval are in the option builder below.' }));
  }

  // audioNodes: mute, volume, loudness, fades, rate and channels.
  audioNodes() {
    const mute = el('input', { type: 'checkbox', id: 'video-mute' });
    mute.addEventListener('change', () => { this.set('mute', mute.checked ? ['1'] : null); this.edited(true); });
    const vol = el('input', { type: 'range', id: 'video-volume', min: -20, max: 20, step: 1, value: 0, 'aria-label': 'Volume (dB)' });
    const volOut = el('output', { id: 'video-volume-out', for: 'video-volume', text: '0 dB' });
    vol.addEventListener('input', () => {
      volOut.textContent = `${Number(vol.value) > 0 ? '+' : ''}${vol.value} dB`;
      this.set('volume', Number(vol.value) ? [`${vol.value}dB`] : null);
      this.edited(false);
    });
    vol.addEventListener('change', () => this.edited(true));
    const ln = el('input', { type: 'checkbox', id: 'video-loudnorm' });
    ln.addEventListener('change', () => { this.set('loudnorm', ln.checked ? ['1'] : null); this.edited(true); });
    const fin = el('input', { type: 'number', id: 'video-fade-in', min: 0, step: 0.1, class: 'num-short', placeholder: '0' });
    const fout = el('input', { type: 'number', id: 'video-fade-out', min: 0, step: 0.1, class: 'num-short', placeholder: '0' });
    const fade = () => {
      const a = Number(fin.value) || 0;
      const b = Number(fout.value) || 0;
      this.set('fade', a || b ? [String(a), String(b)] : null);
      this.edited(true);
    };
    fin.addEventListener('change', fade);
    fout.addEventListener('change', fade);
    const sr = el('select', { id: 'video-rate' }, el('option', { value: '', text: 'source' }), ...SAMPLE_RATES.map((r) => el('option', { value: r, text: `${r / 1000} kHz` })));
    sr.addEventListener('change', () => { this.set('sample_rate', sr.value ? [sr.value] : null); this.edited(true); });
    const ch = el('select', { id: 'video-channels' }, el('option', { value: '', text: 'source' }), el('option', { value: '1', text: 'mono' }), el('option', { value: '2', text: 'stereo' }));
    ch.addEventListener('change', () => { this.set('audio_channels', ch.value ? [ch.value] : null); this.edited(true); });
    const fx = this.caps.audio_effects;
    for (const x of [vol, ln, fin, fout]) {
      x.disabled = !fx;
      if (!fx) x.title = 'This build has no audio effects (volume, loudnorm, afade).';
    }
    const box = el('fieldset', { class: 'vgroup', id: 'video-audio' }, el('legend', { text: 'Audio' }),
      el('p', { class: 'hint', id: 'video-audio-none', hidden: true, text: 'The source has no audio stream.' }),
      el('div', { class: 'row wrap' }, el('label', { class: 'inline', for: 'video-mute' }, mute, ' mute (drop the audio stream)')),
      el('div', { class: 'row wrap' }, el('label', { class: 'inline', for: 'video-volume', text: 'Volume' }), vol, volOut),
      el('div', { class: 'row wrap' }, el('label', { class: 'inline', for: 'video-loudnorm' }, ln, ' loudness normalisation (EBU R128, -23 LUFS)')),
      el('div', { class: 'vgrid' },
        el('div', {}, el('label', { for: 'video-fade-in', text: 'Fade in (s)' }), fin),
        el('div', {}, el('label', { for: 'video-fade-out', text: 'Fade out (s)' }), fout),
        el('div', {}, el('label', { for: 'video-rate', text: 'Sample rate' }), sr),
        el('div', {}, el('label', { for: 'video-channels', text: 'Channels' }), ch)));
    return box;
  }

  // overlayNodes: one-click overlays and the waveform picture.
  overlayNodes() {
    const c = this.caps;
    const local = (this.meta.sources ?? []).some((s) => s.name === 'local');
    const logo = el('button', { type: 'button', class: 'small', id: 'video-add-logo', 'aria-pressed': 'false', text: 'Logo overlay',
      title: `Adds wmu ${WM_FIXTURE} with wm, overlay_window and overlay_fade: edit them in the Watermarks group below` });
    if (!c.overlays) logo.title = 'This build has no overlay filters.';
    else if (!local) logo.title = 'Needs the "local" fs source on testdata/fixtures (compose.playground.yml).';
    logo.addEventListener('click', () => {
      if (this.entry('watermark')) {
        for (const n of ['watermark', 'watermark_url', 'overlay_window', 'overlay_fade']) this.set(n, null);
      } else {
        const [a, b] = this.cut();
        const len = (b ?? a + 4) - a;
        this.set('watermark_url', [WM_FIXTURE]);
        this.set('watermark', ['0.9', 'soea', '16', '16', '0.15']);
        if (len > 1) this.set('overlay_window', [round(len * 0.2), round(len * 0.9)]);
        this.set('overlay_fade', ['0.3', '0.3']);
        toast('Logo overlay added: wmu, wm, overlay_window and overlay_fade.');
      }
      this.edited(true);
    });
    const tc = el('input', { type: 'checkbox', id: 'video-timecode' });
    if (!c.timecode) tc.title = 'This build has no drawtext filter.';
    tc.addEventListener('change', () => { this.set('timecode', tc.checked ? ['hms', 'sowe', '12', '12', '24'] : null); this.edited(true); });
    const wf = el('input', { type: 'checkbox', id: 'video-waveform', disabled: !c.containers.includes('png') });
    const color = el('input', { type: 'color', id: 'video-wf-color', value: '#2266cc', 'aria-label': 'Waveform colour' });
    const bg = el('input', { type: 'color', id: 'video-wf-bg', value: '#ffffff', 'aria-label': 'Waveform background' });
    const transparent = el('input', { type: 'checkbox', id: 'video-wf-transparent', checked: true });
    const split = el('input', { type: 'checkbox', id: 'video-wf-split' });
    const writeWf = () => {
      this.set('waveform', ['1', color.value.slice(1), transparent.checked ? '' : bg.value.slice(1), split.checked ? '1' : '0']);
      this.edited(true);
    };
    wf.addEventListener('change', () => {
      if (wf.checked) {
        this.setContainer('png');
        if (!this.entry('width') && !this.entry('resize')) this.set('resize', ['fit', '800', '200']);
        writeWf();
        return;
      }
      this.set('waveform', null);
      this.setContainer(this.kind() === 'audio' ? 'mp3' : 'mp4');
      this.edited(true);
    });
    for (const x of [color, bg, transparent, split]) x.addEventListener('change', () => { if (wf.checked) writeWf(); });
    return el('fieldset', { class: 'vgroup' }, el('legend', { text: 'Overlays and waveform' }),
      el('div', { class: 'row wrap', id: 'video-overlay-row' }, logo,
        el('label', { class: 'inline', for: 'video-timecode' }, tc, ' running timecode')),
      el('div', { class: 'row wrap' }, el('label', { class: 'inline', for: 'video-waveform' }, wf, ' waveform picture (PNG)'),
        color, el('label', { class: 'inline', for: 'video-wf-transparent' }, transparent, ' transparent'), bg,
        el('label', { class: 'inline', for: 'video-wf-split' }, split, ' one band per channel')),
      el('p', { class: 'hint', text: 'Overlays draw on video outputs only; a waveform draws the cut of the audio as a picture (width × height, 1024×256 by default).' }));
  }

  // sync reflects the pipeline in the controls (the builder may have
  // changed it).
  sync() {
    const body = $('video-body');
    if (!body || body.hidden || !body.childElementCount) return;
    const d = this.duration();
    const [a, b] = this.cut();
    const start = $('trim-start');
    const end = $('trim-end');
    const max = Number(start.max);
    const focused = document.activeElement;
    if (focused !== start) start.value = String(a);
    if (focused !== end) end.value = String(b ?? max);
    const tl = $('timeline');
    tl.style.setProperty('--a', `${(a / max) * 100}%`);
    tl.style.setProperty('--b', `${((b ?? max) / max) * 100}%`);
    if (focused !== $('trim-start-n')) $('trim-start-n').value = a ? round(a) : '';
    if (focused !== $('trim-end-n')) $('trim-end-n').value = this.get('end') !== null || this.get('duration') !== null ? round(b) : '';
    $('trim-readout').textContent = b !== null ? `${fmtSec(b - a)} of ${d ? fmtSec(d) : '?'}` : `from ${fmtSec(a)}`;
    const c = this.container();
    const ext = $('extension');
    if (ext && this.grammar() === 'imgproxy' && focused !== ext) ext.value = this.state.ext ?? '';
    const sel = $('video-container');
    if ([...sel.options].some((o) => o.value === c)) sel.value = c;
    const info = CONTAINERS[c] ?? CONTAINERS.mp4;
    const vc = $('video-vcodec');
    vc.value = this.get('video_codec') ?? '';
    for (const o of vc.options) {
      if (!o.value) continue;
      const fits = (VIDEO_CODECS[c] ?? []).includes(o.value);
      const built = this.caps.codecs.includes(o.value);
      o.disabled = !fits || !built;
      o.textContent = `${o.value}${!built ? ' (not in this image)' : !fits ? ` (not in ${c})` : ''}`;
    }
    vc.disabled = !info.video;
    const ac = $('video-acodec');
    ac.value = this.get('audio_codec') ?? '';
    for (const o of ac.options) {
      if (!o.value) continue;
      const fits = (AUDIO_CODECS[c] ?? []).includes(o.value);
      const built = this.caps.codecs.includes(o.value);
      o.disabled = !fits || !built;
      o.textContent = `${o.value}${!built ? ' (not in this image)' : !fits ? ` (not in ${c})` : ''}`;
    }
    ac.disabled = Boolean(info.image);
    const vcodec = this.get('video_codec') ?? (c === 'mp4' ? this.mp4Codec() : 'vp9');
    $('video-plays').textContent = info.image ? 'A picture of the audio (waveform), not a media file.'
      : canPlay(c, vcodec) ? `This browser plays ${c}${info.video ? ` (${vcodec})` : ''}.`
        : `This browser cannot play ${c}${info.video ? ` (${vcodec})` : ''}: the file is still produced (copy the URL or curl); pick WebM to watch it here.`;
    const set = (id, v) => { const x = $(id); if (x && focused !== x) x.value = v ?? ''; };
    set('video-quality', this.get('quality'));
    set('video-fps', this.get('fps'));
    const rs = this.entry('resize')?.values ?? [];
    set('video-width', this.get('width') ?? (rs[1] && rs[1] !== '0' ? rs[1] : ''));
    set('video-height', this.get('height') ?? (rs[2] && rs[2] !== '0' ? rs[2] : ''));
    if (rs[0]) $('video-fit').value = rs[0];
    // Audio.
    const audio = this.hasAudio();
    $('video-audio-none').hidden = audio;
    $('video-mute').checked = ['1', 'true', 't'].includes(this.get('mute') ?? '');
    $('video-mute').disabled = !audio || !info.video;
    const vol = this.get('volume');
    const db = vol === null ? 0 : /db$/i.test(vol) ? Number(vol.slice(0, -2)) : Math.round(20 * Math.log10(Number(vol) || 1));
    if (focused !== $('video-volume')) $('video-volume').value = String(db);
    $('video-volume-out').textContent = `${db > 0 ? '+' : ''}${db} dB`;
    $('video-loudnorm').checked = ['1', 'true', 't'].includes(this.get('loudnorm') ?? '');
    set('video-fade-in', this.get('fade', 0));
    set('video-fade-out', this.get('fade', 1));
    $('video-rate').value = this.get('sample_rate') ?? '';
    $('video-channels').value = this.get('audio_channels') ?? '';
    // Overlays.
    $('video-add-logo').setAttribute('aria-pressed', String(Boolean(this.entry('watermark'))));
    $('video-add-logo').textContent = this.entry('watermark') ? 'Remove the overlay' : 'Logo overlay';
    $('video-timecode').checked = Boolean(this.entry('timecode')) && this.get('timecode') !== 'none';
    $('video-waveform').checked = Boolean(info.image);
    // Overlays need a video output and the filters (and the fixture logo
    // for the one-click overlay).
    const local = (this.meta.sources ?? []).some((s) => s.name === 'local');
    const logo = $('video-add-logo');
    logo.disabled = !info.video || !this.caps.overlays || (!local && !this.entry('watermark'));
    const tc = $('video-timecode');
    tc.disabled = !info.video || !this.caps.timecode;
    if (!info.video) logo.title = tc.title = 'Overlays draw on video outputs only.';
  }
}
