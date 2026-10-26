# ADR 0015: The documentation website is Astro Starlight, built from the repository's Markdown

- Status: accepted (WP-156)
- Date: 2026-10-26
- Amends: ADR 0007 decision 6 and its WP-81b site layout (the Pages index), ADR 0009 decision 3 (a second, separate Bun project)

## Context

GitHub Pages (`https://me.xantiagoma.com/xtandard-image/`, the custom domain of `xantiagoma.github.io`) served a single link page, the Scalar API page and the JSON Schema files (WP-81b). The documentation itself, about 7,600 lines of Markdown in `docs/`, the root files and `clients/*/README.md`, could only be read on GitHub: no search, no navigation between documents, and nothing a user can bookmark outside the repository view.

Constraints:

- The Markdown in the repository stays the single source of truth and keeps rendering on GitHub, where it uses relative links (`CONFIG.md#x`, `../README.md`, `adr/0006-….md`, `../deploy/`). No document moves or gets a copy to maintain.
- `/schema/v<N>/config.schema.json` is the config schema's `$id`. SchemaStore and every editor modeline depend on it, so the schema URLs and `/api/` must keep serving the same bytes.
- JavaScript tooling runs on Bun (ADR 0009), and npm packages follow the ADR 0006 allowlist, checked by `scripts/check-ts-licenses.ts`.
- The TypeScript client's workspace and its `ts` workflow must not change behaviour.

## Options

| Option | For | Against |
|---|---|---|
| **Astro Starlight** | Pagefind search, sidebar, light/dark themes, an edit link and an in-page table of contents built in; Markdown pages with frontmatter; an MIT link-validation plugin (`starlight-links-validator`); runs on Bun | A Node toolchain (~290 packages) for one static site |
| VitePress | Small, fast, local search | Vue; no first-party link validation; theming the brand takes more work |
| Docusaurus | Mature, versioned docs | React plus a much larger tree; MDX v3 is stricter with the existing Markdown (raw `<` and `{`) |
| MkDocs Material | Excellent search and navigation | Python toolchain next to Bun; the free edition's licence is fine, but its insiders features are paid |
| Hand-written static generator | No dependencies | Search, themes, TOC and accessibility would all be ours to build |

## Decision

1. **Generator: Astro Starlight in `site/`**, a separate Bun project with its own `package.json` and `bun.lock`, like the `deploy/*` Node tooling. Exact pins: `astro` 7.4.2, `@astrojs/starlight` 0.42.7 and `starlight-links-validator` 0.27.0, all MIT, all `devDependencies` of a private package. Astro runs on the Bun runtime (`bun --bun astro build`).
   - **Not a member of the root workspace.** The root scripts fan out with `bun run --filter '*' <script>`, so a member named `build` or `test` would run the site from `bun run build` and `bun run test` in the `ts` workflow, and the root `bun.lock` (installed by `ts` and `clients`) would grow by Astro's tree, sharp included. As a separate project the TypeScript client, the root lockfile and the `ts` workflow do not change.
2. **Sync, not move.** `site/scripts/sync.ts` (a pre-build step) copies each file named in `site/manifest.ts` into `site/src/content/docs/` (generated, gitignored) and adapts the copy:
   - the H1 becomes the page `title` and is removed, with a logo line above it (the client READMEs); the first paragraph becomes `description`; `editUrl` opens the source file on GitHub;
   - relative links to files that are pages become site routes under the base, with their `#fragment`; absolute `github.com/…/blob/main/` links to such files (the client READMEs use them) become routes too;
   - relative links to other repository files and directories become absolute GitHub URLs (`blob/main/…`, `tree/main/…`), and images become raw URLs or, for `docs/assets/`, the site's copy;
   - a relative link to a file that does not exist fails the sync; a Markdown file under `docs/` that is not in the manifest fails it too;
   - GitHub alerts (`> [!WARNING]`) become Starlight asides.
   Fenced code and inline code spans are never rewritten. Long documents are not split: Starlight's in-page table of contents (H2 and H3) does that job, and the GitHub versions stay intact.
3. **Link checks.** The build fails on a broken internal link at three levels: the sync (missing files), `starlight-links-validator` (every Markdown link and `#fragment`, relative links forbidden), and `site/scripts/check-dist.ts` after the build (every local `href`, `src` and `srcset` in every HTML file, including the sidebar, the home page and links into the static files, and every fragment against the target page's ids).
4. **Static files stay byte for byte.** `scripts/pages-build.sh` keeps its `$id` check and now assembles only the non-Astro files (`schema/v<N>/…`, `schema/latest/…`, `api/index.html`, `docs/assets/*`, `.nojekyll`) into `dist/pages-static/`, which is Astro's `publicDir`. `check-dist.ts` compares each of them with the built site. The old link page (`docs/pages/index.html`) becomes the site page "Config schema and OpenAPI files" (`site/content/reference/schemas.md`).
5. **Site-only pages are few:** the home page (`site/content/index.mdx`), the schema reference and the ADR overview (`site/content/`). Everything else comes from the repository.
6. **Internal documents** (`AGENT-WORKFLOW.md`, `go-standards.md`, `docs/research/`, `docs/playground/RESEARCH.md`) are published under Project → Contributing → Internals, collapsed, with a banner, and excluded from search. Public documents link to them (FORMATS cites the stack research, CONTRIBUTING the Go standards), so leaving them out would send readers to GitHub mid-page, while search results for users stay free of them.
7. **Base and origin.** `site` is `https://me.xantiagoma.com` and `base` is `/xtandard-image` by default; `pages.yml` passes the origin and base that `actions/configure-pages` reports (`SITE_ORIGIN`, `SITE_BASE`), so a domain change needs no code change. The schema `$id` keeps `xantiagoma.github.io`, which redirects to the custom domain.
8. **CI.** `pages.yml` builds the site with Bun on pull requests that touch the docs or the site (no deploy) and on pushes to `main`, where it deploys. `task pages:build` and `task pages:dev` run the same steps locally.

## Licences

`bun run licenses` in `site/` runs `scripts/check-ts-licenses.ts site`. Beyond the ADR 0006 list it accepts, for this run-only build tree only, the licences `deploy/check/licenses.ts` accepts for its tools: BlueOak-1.0.0 (`lru-cache`, `sax`, `common-ancestor-path`), CC0-1.0 (`mdn-data`, data) and MPL-2.0 (`lightningcss`, the CSS minifier; file-level copyleft, which ADR 0006 allows even when shipped). sharp's prebuilt libvips (`@img/sharp-libvips-*`, LGPL-3.0-or-later, one package per platform) is a named exception: it is dynamically loaded, which ADR 0006 allows even in shipped artefacts, and here it only runs during the build. The published site contains only the HTML, CSS and JS that Astro and Starlight (MIT) generate, Pagefind's index and runtime (MIT), and the repository's own files.

## Consequences

- The documentation has search, navigation and a stable URL per document, without a second copy of any document.
- A new document under `docs/` fails the site build until it gets a place in `site/manifest.ts`; a new client directory fails it until it gets a sidebar label.
- A broken link or anchor in any synced document now fails a pull request, also in documents that had broken links before (the WP-156 build found one in ROADMAP).
- Astro moves fast (7.x at the time of writing); the exact pins and the lockfile keep builds reproducible, and upgrades are deliberate.
- No custom web fonts: the brand fonts (Urbanist, JetBrains Mono) are OFL-1.1, outside the npm allowlist, and the site would otherwise load them from a third-party CDN. The logo lockups are SVG paths and need no font.
