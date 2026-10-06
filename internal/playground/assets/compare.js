// Media comparison (WP-128): in the video mode the Source side plays the
// original file (served same-origin by /video/…/raw:1/… through
// api/render?ranges=1, so it seeks with byte ranges) and the Result side
// the transformation of it. One control bar under the stage drives both:
// play/pause, seek, speed, mute and an A/B switch of which one is heard.
//
// Timelines: the result's time t is the source's start + t × scale, where
// start is the trim's start and scale is 1 unless the result's duration
// differs from the trimmed window by more than 5 % (a speed change): then
// it is the window over the result's duration. The source's trim window is
// highlighted on the bar's source timeline (every mode) and on the source
// waveform (audio).
//
// Sync: the result is the master; its play, pause, seek, rate and loop
// events move the source (linked). While both play, a frame callback
// measures the drift (source − mapped result time) and smooths it (an
// exponential average over about five callbacks: currentTime advances in
// steps, audio clocks in chunks): beyond one frame (1/fps; 100 ms
// for audio) the source's playback rate is nudged (at most ±10 %), and a
// raw drift beyond max(0.25 s, four frames) seeks it. More than five hard re-syncs in ten seconds (a
// source that seeks slowly in this browser: long GOPs, another codec)
// unlinks the two with a note. A source the browser cannot play falls back
// to its still (the poster) and the result plays alone. Unlinked, each
// player has its own native controls.

import { $, el } from './dom.js';

const reducedMotion = () => matchMedia('(prefers-reduced-motion: reduce)').matches;

// fmtT writes seconds as m:ss.cc.
export const fmtT = (s) => {
  if (!Number.isFinite(s) || s < 0) return '–';
  const m = Math.floor(s / 60);
  return `${m}:${(s - m * 60).toFixed(2).padStart(5, '0')}`;
};

const clamp = (v, a, b) => Math.min(b, Math.max(a, v));
const isMedia = (m) => Boolean(m) && (m.tagName === 'VIDEO' || m.tagName === 'AUDIO');

// Sync thresholds (seconds) for a frame duration f.
export const NUDGE_MAX = 0.1; // the largest playback-rate correction (±10 %)
export const hardDrift = (f) => Math.max(0.25, 4 * f);

// waveBox is the waveform area of an audio pane: the picture (the source's
// server-rendered waveform, or a canvas drawn here), the trim window and a
// playhead.
export function waveBox(picture, { trim = false } = {}) {
  return el('div', { class: 'wave-box', 'aria-hidden': 'true' }, picture,
    trim ? el('div', { class: 'wave-trim' }) : null, el('div', { class: 'wave-head' }));
}

// drawWave draws the peaks of an audio file (a Blob) into a new canvas;
// null when the browser cannot decode it.
export async function drawWave(blob, width = 960, height = 240) {
  let buf;
  try {
    const ctx = new OfflineAudioContext(1, 1, 44100);
    buf = await ctx.decodeAudioData(await blob.arrayBuffer());
  } catch {
    return null;
  }
  const canvas = el('canvas', { class: 'wave', width, height, role: 'img', 'aria-label': 'Waveform of the result' });
  const g = canvas.getContext('2d');
  const color = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#2456c7';
  g.fillStyle = color;
  // One band per channel, like the source's server waveform (wf …:1).
  const chans = [...Array(buf.numberOfChannels).keys()].map((c) => buf.getChannelData(c));
  const band = height / chans.length;
  const n = buf.length;
  const per = Math.max(1, Math.floor(n / width));
  chans.forEach((d, c) => {
    for (let x = 0; x < width; x++) {
      let lo = 0;
      let hi = 0;
      const from = Math.floor((x * n) / width);
      for (let i = from; i < Math.min(n, from + per); i++) {
        if (d[i] < lo) lo = d[i];
        if (d[i] > hi) hi = d[i];
      }
      const y0 = c * band + ((1 - hi) / 2) * band;
      const y1 = c * band + ((1 - lo) / 2) * band;
      g.fillRect(x, y0, 1, Math.max(1, y1 - y0));
    }
  });
  return canvas;
}

export class MediaCompare {
  // onSourceError(src) replaces a source the browser cannot play with its
  // still.
  constructor({ onSourceError } = {}) {
    this.onSourceError = onSourceError;
    this.src = null;
    this.res = null;
    this.linked = true;
    this.listen = 'result';
    this.userMuted = null; // the Sound button's choice; null: videos start muted, audio not
    this.rate = 1;
    this.timing = { start: 0, end: null, duration: 0, fps: 0 };
    this.drift = 0; // smoothed drift (s)
    this.fresh = true; // the next sample restarts the average
    this.resyncs = [];
    this.ac = null;
    this.raf = 0;
    this.seeking = false; // the seek range is being dragged
    this.wire();
  }

  // ---- the bar -------------------------------------------------------------

  wire() {
    $('mb-play').addEventListener('click', () => this.toggle());
    const seek = $('mb-seek');
    seek.addEventListener('input', () => { this.seeking = true; this.seek(Number(seek.value)); });
    seek.addEventListener('change', () => { this.seeking = false; this.seek(Number(seek.value)); });
    $('mb-rate').addEventListener('change', (e) => this.setRate(Number(e.target.value)));
    $('mb-link').addEventListener('change', (e) => this.setLinked(e.target.checked));
    $('mb-ab').addEventListener('click', () => this.ab());
    $('mb-mute').addEventListener('click', () => { this.userMuted = !this.muted; this.applyAudio(); this.ui(); });
    // Clicking a waveform seeks there.
    for (const which of ['before', 'after']) {
      $(`${which}-media`).addEventListener('pointerdown', (e) => {
        const box = e.target.closest('.wave-box');
        if (!box || e.button !== 0) return;
        const r = box.getBoundingClientRect();
        this.seekAt(which, clamp((e.clientX - r.left) / r.width, 0, 1));
      });
    }
  }

  // muted: the Sound button's choice, else videos muted (they autoplay)
  // and audio not.
  get muted() { return this.userMuted ?? $('stage').dataset.media !== 'audio'; }

  // active reports a source and a result that play together.
  active() { return Boolean(this.src && this.res); }

  // bind takes the media elements of both panes (null when a pane holds
  // no media, is loading or is hidden). Rebinding the same pair only
  // refreshes the bar.
  bind(src, res, { loading = false } = {}) {
    src = isMedia(src) ? src : null;
    res = isMedia(res) ? res : null;
    if (src !== this.src || res !== this.res) {
      this.ac?.abort();
      this.ac = null;
      cancelAnimationFrame(this.raf);
      this.raf = 0;
      if (this.src && this.src !== src) this.src.pause();
      if (!res && src && this.res && this.linked) src.pause(); // the result is being replaced
      this.src = src;
      this.res = res;
      this.resyncs = [];
      if (src && res) this.listenTo(src, res);
    }
    const stage = $('stage');
    stage.dataset.media = src?.tagName === 'AUDIO' || res?.tagName === 'AUDIO' ? 'audio' : src || res ? 'video' : '';
    const bar = $('media-bar');
    bar.hidden = !src || (!res && !loading);
    this.controls();
    this.applyAudio();
    if (this.active() && this.linked) {
      this.syncTo();
      if (!res.paused) this.src.play().catch(() => {});
    }
    this.ui();
    this.kick();
  }

  // setTiming takes the trim and the source facts: {start, end, duration, fps}.
  setTiming(t) {
    if (!t) return;
    this.timing = { start: t.start || 0, end: t.end ?? null, duration: t.duration || 0, fps: t.fps || 0 };
    if (this.active() && this.linked) this.syncTo();
    this.ui();
  }

  // ---- timelines -----------------------------------------------------------

  srcDuration() {
    const d = this.src?.duration;
    return this.timing.duration || (Number.isFinite(d) ? d : 0);
  }

  resDuration() {
    const d = this.res?.duration;
    return Number.isFinite(d) ? d : 0;
  }

  // scale is source seconds per result second.
  scale() {
    const { start, end } = this.timing;
    const e = end ?? this.srcDuration();
    const d = this.resDuration();
    if (e && d) {
      const r = (e - start) / d;
      if (Math.abs(r - 1) > 0.05 && r > 0.1 && r < 10) return r;
    }
    return 1;
  }

  map(t) { return this.timing.start + t * this.scale(); }

  unmap(s) { return (s - this.timing.start) / this.scale(); }

  // frame is the sync granularity: a video frame, or 100 ms of audio (an
  // audio clock's currentTime moves in uneven steps of tens of ms).
  frame() {
    if (this.timing.fps) return 1 / this.timing.fps;
    return this.src?.tagName === 'AUDIO' || this.res?.tagName === 'AUDIO' ? 0.1 : 1 / 30;
  }

  // ---- linking ---------------------------------------------------------------

  listenTo(src, res) {
    this.ac = new AbortController();
    const o = { signal: this.ac.signal };
    res.addEventListener('play', () => {
      if (!this.linked) return;
      this.syncTo();
      this.src.play().catch(() => {});
      this.kick();
    }, o);
    res.addEventListener('pause', () => { if (this.linked) this.src.pause(); this.ui(); }, o);
    res.addEventListener('seeking', () => { if (this.linked) this.syncTo(); this.ui(); }, o);
    res.addEventListener('ratechange', () => {
      this.rate = res.playbackRate;
      if (this.linked) this.src.playbackRate = this.rate * this.scale();
      this.ui();
    }, o);
    for (const ev of ['timeupdate', 'loadedmetadata', 'durationchange']) res.addEventListener(ev, () => this.ui(), o);
    src.addEventListener('loadedmetadata', () => this.ui(), o);
    src.addEventListener('play', () => this.kick(), o);
    src.addEventListener('timeupdate', () => { if (!this.linked) this.ui(); }, o);
    src.addEventListener('error', () => this.onSourceError?.(src), o);
  }

  // syncTo puts the source at the result's mapped time.
  syncTo() {
    if (!this.active()) return;
    const d = this.srcDuration();
    const want = clamp(this.map(this.res.currentTime), 0, d || Infinity);
    if (Math.abs(this.src.currentTime - want) > 0.001) this.src.currentTime = want;
    this.src.playbackRate = this.rate * this.scale();
    this.fresh = true;
  }

  setLinked(on) {
    this.linked = on;
    $('mb-link').checked = on;
    if (this.active()) {
      if (on) {
        this.syncTo();
        if (this.res.paused) this.src.pause();
        else this.src.play().catch(() => {});
      } else {
        this.src.playbackRate = this.rate;
      }
    }
    this.controls();
    this.ui();
    this.kick();
  }

  // controls shows the native controls of a player the bar does not drive.
  controls() {
    const driven = this.active() && this.linked;
    for (const m of [this.src, this.res]) if (m) m.controls = !driven;
    $('stage').classList.toggle('media-native', !driven);
  }

  // ---- actions ---------------------------------------------------------------

  toggle() {
    if (!this.res) return;
    if (this.res.paused) this.res.play().catch(() => {});
    else this.res.pause();
  }

  // seek moves the result to t (the source follows when linked).
  seek(t) {
    if (!this.res) return;
    this.res.currentTime = clamp(t, 0, this.resDuration() || 0);
    if (this.linked) this.syncTo();
    this.ui();
  }

  // seekAt seeks to a fraction of a pane's waveform: on the source's, a
  // time outside the trim moves the source alone (unlinked) or clamps.
  seekAt(which, frac) {
    if (which === 'after') {
      this.seek(frac * this.resDuration());
      return;
    }
    const s = frac * this.srcDuration();
    if (this.active() && this.linked) this.seek(clamp(this.unmap(s), 0, this.resDuration()));
    else if (this.src) this.src.currentTime = s;
    this.ui();
  }

  setRate(r) {
    this.rate = r;
    if (this.res) this.res.playbackRate = r;
    if (this.src) this.src.playbackRate = r * (this.linked ? this.scale() : 1);
    this.ui();
  }

  // ab switches which one is heard, at the same position: linked, both
  // keep playing and the mute moves; unlinked, the one heard so far stops
  // and the other starts at its mapped time. Switching unmutes.
  ab() {
    if (!this.active()) return;
    const from = this.listen === 'result' ? this.res : this.src;
    const to = from === this.res ? this.src : this.res;
    this.listen = this.listen === 'result' ? 'source' : 'result';
    if (!this.linked) {
      to.currentTime = to === this.src ? clamp(this.map(from.currentTime), 0, this.srcDuration() || Infinity)
        : clamp(this.unmap(from.currentTime), 0, this.resDuration() || Infinity);
      if (!from.paused) {
        from.pause();
        to.play().catch(() => {});
      }
    }
    this.userMuted = false;
    this.applyAudio();
    this.ui();
  }

  // applyAudio mutes the one not heard (an unpaired player keeps its own
  // mute).
  applyAudio() {
    if (!this.active()) return;
    this.src.muted = this.muted || this.listen !== 'source';
    this.res.muted = this.muted || this.listen !== 'result';
  }

  // ---- the frame loop ----------------------------------------------------------

  kick() {
    if (!this.raf && this.src && this.res) this.raf = requestAnimationFrame(() => this.tick());
  }

  tick() {
    this.raf = 0;
    if (!this.active()) return;
    const { src, res } = this;
    if (this.linked && !res.paused && !src.seeking && !res.seeking && src.readyState >= 2) {
      const want = this.map(res.currentTime);
      const d = this.srcDuration();
      if (d && want >= d) {
        src.pause();
      } else {
        if (src.paused) src.play().catch(() => {});
        const raw = src.currentTime - want;
        const f = this.frame();
        const base = this.rate * this.scale();
        this.drift = this.fresh ? raw : this.drift * 0.8 + raw * 0.2;
        this.fresh = false;
        if (Math.abs(raw) > hardDrift(f)) {
          src.currentTime = want;
          src.playbackRate = base;
          this.fresh = true;
          this.resynced();
        } else if (Math.abs(this.drift) > f) {
          src.playbackRate = base * (1 - clamp(this.drift * 2, -NUDGE_MAX, NUDGE_MAX));
        } else if (Math.abs(this.drift) < f / 2 && src.playbackRate !== base) {
          src.playbackRate = base;
        }
      }
    } else if (this.linked && res.paused) {
      this.drift = src.currentTime - this.map(res.currentTime);
    }
    this.ui();
    if (!res.paused || !src.paused) this.raf = requestAnimationFrame(() => this.tick());
  }

  // resynced counts hard re-syncs; too many unlink the players.
  resynced() {
    const now = performance.now();
    this.resyncs = this.resyncs.filter((t) => now - t < 10000);
    this.resyncs.push(now);
    if (this.resyncs.length > 5) {
      this.setLinked(false);
      this.note('The two could not stay in sync (the source seeks slowly in this browser: a long keyframe interval or another codec), so they are unlinked: play each with its own controls.');
    }
  }

  note(text) { $('mb-status').textContent = text; }

  // ---- what the bar shows ------------------------------------------------------

  ui() {
    const bar = $('media-bar');
    if (bar.hidden) return;
    const { src, res } = this;
    const on = this.active();
    const rd = this.resDuration();
    const play = $('mb-play');
    const playing = res && !res.paused;
    play.textContent = playing ? 'Pause' : 'Play';
    play.setAttribute('aria-label', `${playing ? 'Pause' : 'Play'} ${on && this.linked ? 'the source and the result' : 'the result'}`);
    play.disabled = !res;
    const seek = $('mb-seek');
    seek.max = String(rd || 0);
    seek.disabled = !res || !rd;
    if (!this.seeking && res) seek.value = String(res.currentTime);
    $('mb-time').textContent = `${fmtT(res?.currentTime ?? 0)} / ${fmtT(rd)}`;
    $('mb-rate').value = String(this.rate);
    $('mb-rate').disabled = !res;
    $('mb-ab').disabled = !on;
    $('mb-link').disabled = !on;
    const ab = $('mb-ab');
    ab.textContent = `Hearing: ${this.muted ? 'nothing' : this.listen}`;
    ab.setAttribute('aria-pressed', String(this.listen === 'source'));
    const mute = $('mb-mute');
    mute.textContent = this.muted ? 'Sound off' : 'Sound on';
    mute.setAttribute('aria-pressed', String(!this.muted));
    mute.disabled = !res;
    // The source timeline: the trim window and both playheads.
    const sd = this.srcDuration();
    const { start } = this.timing;
    const end = this.timing.end ?? sd;
    const pct = (s) => `${sd ? clamp((s / sd) * 100, 0, 100) : 0}%`;
    const tl = $('mb-timeline');
    tl.style.setProperty('--a', pct(start));
    tl.style.setProperty('--w', sd && end ? `${clamp(((end - start) / sd) * 100, 0, 100)}%` : '100%');
    tl.style.setProperty('--s', pct(src?.currentTime ?? start));
    tl.style.setProperty('--r', pct(res ? this.map(res.currentTime) : start));
    tl.classList.toggle('unknown', !sd);
    const k = this.scale();
    $('mb-trim').textContent = sd
      ? `Source timeline (${fmtT(sd)}): the result is ${fmtT(start)}–${fmtT(end)} of it (highlighted)${k !== 1 ? `, played ×${(1 / k).toFixed(2)}` : ''}.`
      : 'Source timeline: duration unknown.';
    const sync = $('mb-sync');
    const synced = on && this.linked;
    sync.textContent = synced ? `Δ ${Math.round(this.drift * 1000)} ms` : on ? 'unlinked' : src ? 'result loading' : '';
    bar.dataset.drift = synced ? String(this.drift) : '';
    bar.dataset.linked = String(synced);
    // Audio panes: the trim on the source waveform and both playheads.
    const wb = $('before-media').querySelector('.wave-box');
    if (wb) {
      wb.style.setProperty('--a', pct(start));
      wb.style.setProperty('--w', sd && end ? `${clamp(((end - start) / sd) * 100, 0, 100)}%` : '100%');
      wb.style.setProperty('--h', pct(src?.currentTime ?? 0));
    }
    const wa = $('after-media').querySelector('.wave-box');
    if (wa && res) wa.style.setProperty('--h', `${rd ? clamp((res.currentTime / rd) * 100, 0, 100) : 0}%`);
  }
}

// autoplays reports whether a result video may start on its own.
export const autoplays = () => !reducedMotion();
