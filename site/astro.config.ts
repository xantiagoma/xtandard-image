// The documentation site (WP-156, ADR 0015): Astro Starlight over the repository's Markdown.
// scripts/sync.ts copies the pages into src/content/docs/ first; site/manifest.ts holds the
// information architecture both use. `task pages:build` builds it into dist/pages/.
import starlight from "@astrojs/starlight";
import { defineConfig } from "astro/config";
import starlightLinksValidator from "starlight-links-validator";
import { isGroup, isPage, repoUrl, sidebar, siteBase, siteOrigin, type Item } from "./manifest.ts";

type SidebarItem =
  | { slug: string; label?: string }
  | { label: string; link: string }
  | { label: string; collapsed?: boolean; items: SidebarItem[] };

const toSidebar = (items: Item[]): SidebarItem[] =>
  items.map((i) => {
    if (isPage(i)) return i.label ? { slug: i.slug, label: i.label } : { slug: i.slug };
    if (isGroup(i)) return { label: i.label, collapsed: i.collapsed, items: toSidebar(i.items) };
    return { label: i.label, link: i.link };
  });

const social = `${siteOrigin}${siteBase}/assets/social.png`;

export default defineConfig({
  site: siteOrigin,
  base: siteBase,
  // "ignore": Starlight would otherwise add a slash after the #fragment of a sidebar link.
  trailingSlash: "ignore",
  outDir: "../dist/pages",
  // scripts/pages-build.sh assembles the files that must stay byte for byte as they are: the
  // schemas at their $id URLs, the Scalar page at /api/, and docs/assets.
  publicDir: "../dist/pages-static",
  telemetry: false,
  integrations: [
    starlight({
      title: "xtandard-image",
      description: "A self-hosted image and video processing server in Go and libvips, with imgproxy-compatible URLs.",
      logo: {
        light: "../docs/assets/lockup-horizontal-light.svg",
        dark: "../docs/assets/lockup-horizontal-dark.svg",
        alt: "xtandard-image",
        replacesTitle: true,
      },
      favicon: "/assets/favicon.svg",
      head: [
        { tag: "link", attrs: { rel: "icon", href: `${siteBase}/assets/favicon.ico`, sizes: "16x16 32x32 48x48" } },
        { tag: "link", attrs: { rel: "apple-touch-icon", href: `${siteBase}/assets/apple-touch-icon.png` } },
        { tag: "meta", attrs: { name: "theme-color", content: "#0F1215" } },
        { tag: "meta", attrs: { property: "og:image", content: social } },
        { tag: "meta", attrs: { property: "og:image:width", content: "1280" } },
        { tag: "meta", attrs: { property: "og:image:height", content: "640" } },
        { tag: "meta", attrs: { property: "og:image:alt", content: "xtandard-image: self-hosted image and video processing proxy" } },
        { tag: "meta", attrs: { name: "twitter:image", content: social } },
      ],
      social: [{ icon: "github", label: "GitHub", href: repoUrl }],
      editLink: { baseUrl: `${repoUrl}/edit/main/` },
      customCss: ["./src/styles/brand.css"],
      tableOfContents: { minHeadingLevel: 2, maxHeadingLevel: 3 },
      sidebar: toSidebar(sidebar),
      plugins: [
        starlightLinksValidator({
          errorOnRelativeLinks: true,
          errorOnInvalidHashes: true,
          // Static files from publicDir, not Starlight pages: scripts/check-dist.ts checks them.
          exclude: [`${siteBase}/api/`, `${siteBase}/schema/**`, `${siteBase}/assets/**`],
        }),
      ],
    }),
  ],
});
