// The site's information architecture: which repository Markdown file becomes which page, and
// the sidebar tree. scripts/sync.ts reads it to copy the files into src/content/docs/, and
// astro.config.ts reads it to build the sidebar, so a page and its sidebar entry cannot drift.
//
// The Markdown in the repository is the single source of truth (ADR 0015). A page is either a
// repository file (`src`, relative to the repository root), a section of one (`section`), or a
// page written for the site only (`site`, relative to site/content/). Plain Node APIs only:
// Astro loads this file through Vite.
import { readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";

export const repoRoot = fileURLToPath(new URL("../", import.meta.url));
export const repoUrl = "https://github.com/xantiagoma/xtandard-image";
export const branch = "main";
/**
 * Where the site is served. GitHub Pages serves the project site of xantiagoma.github.io under
 * the account's custom domain (xantiagoma.github.io redirects there), so the origin is the custom
 * domain and the base is the repository name. pages.yml passes the values actions/configure-pages
 * reports (SITE_ORIGIN, SITE_BASE), so a domain change needs no code change.
 */
export const siteOrigin = process.env.SITE_ORIGIN || "https://me.xantiagoma.com";
export const siteBase = (process.env.SITE_BASE || "/xtandard-image").replace(/\/+$/, "");
/** The pinned image on the home page. */
export const image = "ghcr.io/xantiagoma/xtandard-image:0.1.1";

export type Page = {
  /** Route without the base and without slashes; "" is the home page. */
  slug: string;
  /** Sidebar label; the page title (the source's H1) when absent. */
  label?: string;
  /** Repository file the page is built from. */
  src?: string;
  /** Only this H2 section of `src` (by its heading text), as its own page. */
  section?: string;
  /** Title of a `section` page (it has no H1 of its own). */
  title?: string;
  /** Site-only page in site/content/ (the home page, the schema reference). */
  site?: string;
  /** Contributor-only material: a banner says so and search skips it. */
  internal?: boolean;
};
export type Link = { label: string; link: string };
export type Group = { label: string; collapsed?: boolean; items: Item[] };
export type Item = Page | Link | Group;

const ls = (dir: string) => readdirSync(`${repoRoot}${dir}`).sort();

/** Every ADR in docs/adr/, in file order. Two ADRs share the number 0010; their slugs differ. */
const adrs: Page[] = ls("docs/adr")
  .filter((f) => /^\d{4}-.*\.md$/.test(f))
  .map((f) => ({ slug: `project/adr/${f.replace(/\.md$/, "")}`, src: `docs/adr/${f}` }));

/** The client READMEs, in the order of the README's client table (TypeScript first). */
const clientOrder = ["typescript", "python", "go", "php", "ruby", "rust", "java", "kotlin", "dotnet", "elixir", "swift", "dart"];
const clientLabels: Record<string, string> = {
  typescript: "TypeScript",
  python: "Python",
  go: "Go",
  php: "PHP",
  ruby: "Ruby",
  rust: "Rust",
  java: "Java",
  kotlin: "Kotlin Multiplatform",
  dotnet: ".NET",
  elixir: "Elixir",
  swift: "Swift",
  dart: "Dart and Flutter",
};
const clientDirs = ls("clients").filter((d) => clientLabels[d] !== undefined);
const unknownClients = ls("clients").filter(
  (d) => !clientLabels[d] && !["codegen", "spec", "README.md"].includes(d),
);
if (unknownClients.length > 0) {
  throw new Error(`site/manifest.ts: add a sidebar label for clients/${unknownClients.join(", clients/")}`);
}
const clients: Page[] = [...clientDirs]
  .sort((a, b) => clientOrder.indexOf(a) - clientOrder.indexOf(b))
  .map((d) => ({ slug: `clients/${d}`, src: `clients/${d}/README.md`, label: clientLabels[d] }));

export const home: Page = { slug: "", site: "index.mdx" };

export const sidebar: Item[] = [
  { slug: "getting-started", src: "docs/GETTING-STARTED.md", label: "Getting started" },
  {
    label: "Guides",
    items: [
      { slug: "guides/url-dialects", src: "docs/URL-DIALECTS.md", label: "URL dialects" },
      { label: "Signing and encryption", link: "/guides/url-dialects/#2-signing-schemes-at-a-glance" },
      { label: "Video and audio", link: "/guides/url-dialects/#38-video-transformations-wp-122-wp-123" },
      { slug: "guides/configuration", src: "docs/CONFIG.md", label: "Configuration" },
      { slug: "guides/sources", src: "docs/SOURCES.md", label: "Sources" },
      { slug: "guides/formats", src: "docs/FORMATS.md", label: "Formats" },
      { slug: "guides/playground", src: "docs/PLAYGROUND.md", label: "Playground" },
    ],
  },
  {
    label: "Deploy and operations",
    items: [
      { slug: "deploy", src: "docs/DEPLOY.md", label: "Deploy" },
      { slug: "operations", src: "docs/OPERATIONS.md", label: "Operations" },
    ],
  },
  {
    label: "Clients",
    items: [{ slug: "clients", src: "clients/README.md", label: "Overview" }, ...clients],
  },
  {
    label: "Reference",
    items: [
      { label: "API reference (Scalar)", link: "/api/" },
      { slug: "reference/schemas", site: "reference/schemas.md", label: "Config schema and OpenAPI files" },
      { slug: "reference/openapi", src: "docs/OPENAPI.md", label: "OpenAPI document" },
      { slug: "reference/comparison", src: "docs/COMPARISON.md", label: "Comparison" },
      { slug: "reference/benchmarks", src: "docs/BENCHMARKS.md", label: "Benchmarks" },
      { slug: "reference/benchmark-results", src: "bench/RESULTS.md", label: "Benchmark results" },
      { slug: "reference/changelog", src: "CHANGELOG.md", label: "Changelog" },
    ],
  },
  {
    label: "Project",
    items: [
      { slug: "project/roadmap", src: "docs/ROADMAP.md", label: "Roadmap" },
      { slug: "project/release", src: "docs/RELEASE.md", label: "Releases" },
      { slug: "project/architecture", src: "docs/ARCHITECTURE.md", label: "Architecture" },
      {
        label: "Decisions (ADRs)",
        collapsed: true,
        items: [{ slug: "project/adr", site: "project/adr.md", label: "Overview" }, ...adrs],
      },
      {
        label: "Contributing",
        items: [
          { slug: "project/contributing", src: "CONTRIBUTING.md", label: "Contributing" },
          { slug: "project/code-of-conduct", src: "CODE_OF_CONDUCT.md", label: "Code of conduct" },
          {
            label: "Internals",
            collapsed: true,
            items: [
              { slug: "project/internals/agent-workflow", src: "docs/AGENT-WORKFLOW.md", label: "Agent workflow", internal: true },
              { slug: "project/internals/go-standards", src: "docs/go-standards.md", label: "Go standards", internal: true },
              { slug: "project/internals/stack-research", src: "docs/research/stack.md", label: "Tech-stack research", internal: true },
              { slug: "project/internals/format-gap", src: "docs/research/format-gap.md", label: "Format gap research", internal: true },
              { slug: "project/internals/playground-research", src: "docs/playground/RESEARCH.md", label: "Playground research", internal: true },
            ],
          },
        ],
      },
      { slug: "project/security", src: "SECURITY.md", label: "Security" },
      { slug: "project/support", src: "SUPPORT.md", label: "Getting help" },
      { slug: "project/brand", src: "docs/BRAND.md", label: "Brand" },
      { slug: "project/disclaimer", src: "README.md", section: "Disclaimer", title: "Disclaimer", label: "Disclaimer" },
    ],
  },
];

export const isPage = (i: Item): i is Page => "slug" in i;
export const isGroup = (i: Item): i is Group => "items" in i;

/** Every page, the home page first. */
export function pages(): Page[] {
  const out: Page[] = [home];
  const walk = (items: Item[]) => {
    for (const i of items) {
      if (isPage(i)) out.push(i);
      else if (isGroup(i)) walk(i.items);
    }
  };
  walk(sidebar);
  return out;
}
