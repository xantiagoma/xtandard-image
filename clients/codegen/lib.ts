// Language-independent helpers for the emitters: how an option's argument
// forms expand into fixed-length shapes, which options accept a bare first
// argument, and name casing. Every emitter should use these, so all
// clients agree on what an options value may look like.

import type { SpecArg, SpecForm, SpecOption } from "./spec.ts";

/** One accepted argument list shape: fixed elements, then optionally a repeated tail. */
export interface Shape {
  /** The leading arguments, in order (each required within this shape). */
  readonly fixed: readonly SpecArg[];
  /** Arguments that may repeat after `fixed` (zero or more times), or undefined. */
  readonly rest?: readonly SpecArg[];
}

/**
 * Expands the forms of an option into shapes: a form with arguments
 * a1..an and min_items m gives the shapes [a1..am], …, [a1..an]; a form
 * with a rest group gives [a1..an, rest…] too (and, when min_items > n,
 * the rest elements that are required).
 */
export function shapes(option: SpecOption): Shape[] {
  const out: Shape[] = [];
  const seen = new Set<string>();
  const add = (s: Shape) => {
    const key = JSON.stringify([s.fixed.map((a) => a.name + a.type), s.rest?.map((a) => a.name + a.type)]);
    if (!seen.has(key)) {
      seen.add(key);
      out.push(s);
    }
  };
  for (const f of option.forms) for (const s of formShapes(f)) add(s);
  return out;
}

function formShapes(f: SpecForm): Shape[] {
  const min = Math.max(f.min_items, 1);
  const n = f.args.length;
  const out: Shape[] = [];
  if (!f.rest || f.rest.length === 0) {
    for (let len = Math.min(min, n); len <= n; len++) out.push({ fixed: f.args.slice(0, len) });
    return out;
  }
  for (let len = Math.min(min, n); len < n; len++) if (len > 0) out.push({ fixed: f.args.slice(0, len) });
  const fixed = [...f.args];
  for (let i = 0; fixed.length < min; i++) fixed.push(f.rest[i % f.rest.length]!);
  out.push({ fixed, rest: f.rest });
  return out;
}

/**
 * The argument types an option may be written with as a bare value (`q: 80`
 * for `q: [80]`): the first argument of every form that accepts a single
 * argument. Empty when every form needs two or more.
 */
export function bareArgs(option: SpecOption): SpecArg[] {
  const out: SpecArg[] = [];
  for (const f of option.forms) {
    if (Math.max(f.min_items, 1) > 1) continue;
    const first = f.args[0] ?? f.rest?.[0];
    if (first) out.push(first);
  }
  return out;
}

/** Every argument an option's forms may contain, in form order. */
export function allArgs(option: SpecOption): SpecArg[] {
  return option.forms.flatMap((f) => [...f.args, ...(f.rest ?? [])]);
}

/** Splits a name on "-", "_" and case changes. */
export function words(name: string): string[] {
  return name
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .split(/[^A-Za-z0-9]+/)
    .filter(Boolean);
}

/** PascalCase of a name ("min-width" → "MinWidth"). */
export function pascal(name: string): string {
  return words(name)
    .map((w) => w[0]!.toUpperCase() + w.slice(1))
    .join("");
}

/** snake_case of a name ("min-width" → "min_width"). */
export function snake(name: string): string {
  return words(name)
    .map((w) => w.toLowerCase())
    .join("_");
}

/** Every name and alias of a catalogue, with the canonical name. */
export function nameTable(options: readonly SpecOption[]): [string, string][] {
  const out: [string, string][] = [];
  for (const o of options) {
    out.push([o.name, o.name]);
    for (const a of o.aliases) out.push([a, o.name]);
  }
  return out.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
}
