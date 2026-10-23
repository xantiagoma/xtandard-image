// A small syntax highlighter for the code snippets (WP-145): one tokenizer
// shared by the thirteen snippet languages (comments, strings, numbers,
// keywords, literals, types, calls, atoms and variables). It only has to
// read the code the snippet printers write, so it is a page of rules, not a
// vendored highlighter and its grammars. tokenize returns [{t, s}]; t is a
// CSS class suffix (tk-<t>) or '' for plain text, and the page builds the
// spans with textContent (no HTML is parsed).

const KEYWORDS = {
  typescript: 'import from export const let var await async function return new if else for of in await typeof',
  python: 'import from as def return if else elif for in with async await not and or is lambda',
  go: 'package import func var const return if else for range defer go type struct map chan err nil',
  php: 'require use function new return echo if else foreach as fn',
  ruby: 'require def end do if else unless return puts module class',
  rust: 'use fn let mut pub struct impl enum match if else for in return Ok Err Some None as ref',
  java: 'import package public private static final class void new return if else for String var',
  kotlin: 'import package fun val var return if else for in object class listOf',
  csharp: 'using var new return if else foreach in static class public async await',
  elixir: 'alias import defmodule def defp do end fn case when',
  swift: 'import let var func try await return if else for in guard',
  dart: 'import void final var const return if else for in async await',
  curl: 'curl',
};
const LITERALS = /^(true|false|null|nil|None|True|False|undefined)$/;

const RULES = {
  hashComment: ['python', 'ruby', 'elixir', 'curl'],
  slashComment: ['typescript', 'go', 'php', 'rust', 'java', 'kotlin', 'csharp', 'swift', 'dart'],
  singleQuoted: ['typescript', 'python', 'php', 'ruby', 'dart', 'curl', 'elixir'],
  atoms: ['ruby', 'elixir'],
  dollarVars: ['php'],
};
const has = (rule, lang) => RULES[rule].includes(lang);

const keywordSets = new Map(Object.entries(KEYWORDS).map(([k, v]) => [k, new Set(v.split(' '))]));

// tokenize splits code into highlighted runs.
export function tokenize(code, lang) {
  const kw = keywordSets.get(lang) ?? new Set();
  const out = [];
  const push = (t, s) => {
    const last = out.at(-1);
    if (last && last.t === t) last.s += s;
    else out.push({ t, s });
  };
  let i = 0;
  const n = code.length;
  while (i < n) {
    const c = code[i];
    const rest = code.slice(i);
    let m;
    // Comments.
    if ((c === '#' && has('hashComment', lang) && !(lang === 'elixir' && code[i - 1] === '\\')) || (c === '/' && code[i + 1] === '/' && has('slashComment', lang))) {
      const end = code.indexOf('\n', i);
      const s = end < 0 ? code.slice(i) : code.slice(i, end);
      push('c', s);
      i += s.length;
      continue;
    }
    if (c === '/' && code[i + 1] === '*' && has('slashComment', lang)) {
      const end = code.indexOf('*/', i + 2);
      const s = end < 0 ? code.slice(i) : code.slice(i, end + 2);
      push('c', s);
      i += s.length;
      continue;
    }
    // Strings: "…", '…' and `…`, with backslash escapes (shell strings
    // have none inside single quotes).
    if (c === '"' || c === '`' || (c === "'" && has('singleQuoted', lang))) {
      let j = i + 1;
      while (j < n && code[j] !== c) {
        if (code[j] === '\\' && !(lang === 'curl' && c === "'")) j++;
        j++;
      }
      push('s', code.slice(i, j + 1));
      i = j + 1;
      continue;
    }
    // Elixir and Ruby atoms (:hls) and keyword keys (base_url:).
    if (has('atoms', lang) && (m = /^:[a-z_][\w?!]*/.exec(rest)) && !/[\w:]/.test(code[i - 1] ?? '')) {
      push('a', m[0]);
      i += m[0].length;
      continue;
    }
    if (has('dollarVars', lang) && (m = /^\$[A-Za-z_]\w*/.exec(rest))) {
      push('v', m[0]);
      i += m[0].length;
      continue;
    }
    if ((m = /^-?\d[\d_]*(\.\d+)?([eE][-+]?\d+)?[LfF]?\b/.exec(rest)) && !/[\w.]/.test(code[i - 1] ?? '')) {
      if (m[0].startsWith('-') && /[\w)\]]/.test(code.slice(0, i).trimEnd().at(-1) ?? '')) {
        push('', '-');
        i++;
        continue;
      }
      push('n', m[0]);
      i += m[0].length;
      continue;
    }
    if ((m = /^[A-Za-z_][\w]*[!?]?/.exec(rest))) {
      const w = m[0];
      const after = code.slice(i + w.length);
      let t = '';
      if (kw.has(w)) t = 'k';
      else if (LITERALS.test(w)) t = 'l';
      else if (has('atoms', lang) && /^:\s/.test(after) && !/[\w:]/.test(code[i - 1] ?? '')) {
        push('a', `${w}:`);
        i += w.length + 1;
        continue;
      } else if (/^\s*\(/.test(after) || (lang === 'kotlin' && /^\s*\{/.test(after))) t = 'f';
      else if (/^[A-Z]/.test(w)) t = 't';
      push(t, w);
      i += w.length;
      continue;
    }
    // Shell flags.
    if (lang === 'curl' && (m = /^-{1,2}[A-Za-z][\w-]*/.exec(rest)) && /\s/.test(code[i - 1] ?? ' ')) {
      push('k', m[0]);
      i += m[0].length;
      continue;
    }
    push('', c);
    i++;
  }
  return out;
}
