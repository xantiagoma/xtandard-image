// Code snippets for curl and the official clients (WP-145).
//
//   builder state ──request.js──▶ request (data)
//                 ──model.js neutral()──▶ language-neutral code model
//                 ──lang/<id>.js refine()──▶ that language's code model
//                 ──lang/<id>.js printer──▶ Doc (doc.js) ──layout──▶ text
//
// Adding a language is one file in lang/: a refiner (domain nodes → the
// client's idiom) and a printer (syntax and leaf tokens). Keys never appear
// in a snippet: the code reads XI_SIGNING_KEY, XI_SIGNING_SALT,
// XI_ENCRYPTION_KEY and XI_PDF_PASSWORD from the environment.
// scripts/playground-snippets-check.ts runs every snippet with its client.

import { neutral, QUERY_LANGUAGES, Unexpressible } from './model.js';
import { curl, program as curlProgram } from './lang/curl.js';
import { typescript } from './lang/typescript.js';
import { python } from './lang/python.js';
import { go } from './lang/go.js';
import { php } from './lang/php.js';
import { ruby } from './lang/ruby.js';
import { rust } from './lang/rust.js';
import { java } from './lang/java.js';
import { kotlin } from './lang/kotlin.js';
import { csharp } from './lang/csharp.js';
import { elixir } from './lang/elixir.js';
import { swift } from './lang/swift.js';
import { dart } from './lang/dart.js';

export { infoModel, snippetModel, typedValue } from './request.js';
export { ENV } from './model.js';

const ALL = [curl, typescript, python, go, php, ruby, rust, java, kotlin, csharp, elixir, swift, dart];
const BY_ID = new Map(ALL.map((l) => [l.id, l]));

export const LANGUAGES = ALL.map(({ id, label }) => ({ id, label }));

// renderSnippet writes request r (snippetModel, infoModel) in lang:
// {code, note}; code is '' when the language cannot express it (the note
// says why). opts.fixedIv reads a fixed IV from XI_ENCRYPTION_IV.
export function renderSnippet(lang, r, opts = {}) {
  if (r.queryMethod && lang !== 'curl' && !QUERY_LANGUAGES.has(lang)) {
    const label = LANGUAGES.find((l) => l.id === lang)?.label ?? lang;
    const get = renderSnippet(lang, { ...r, queryMethod: false }, opts);
    const why = `The ${label} snippet builds the GET URL: send it as QUERY with the client's query method request helper (its README, "HTTP QUERY").`;
    return { code: get.code, note: [get.note, why].filter(Boolean).join(' ') };
  }
  const note = r.note ?? '';
  if (lang === 'curl') return { code: r.url ? curl.printer.print(curlProgram(r)).trimEnd() : '', note };
  if (r.kind === 'none') return { code: '', note };
  const l = BY_ID.get(lang);
  if (!l) return { code: '', note: `No ${lang} snippets.` };
  try {
    const program = l.refine(neutral(r, { fixedIv: Boolean(opts.fixedIv) }));
    return { code: l.printer.print(program).trimEnd(), note };
  } catch (err) {
    if (err instanceof Unexpressible) return { code: '', note: err.message };
    throw err;
  }
}
