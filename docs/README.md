# Formbricks Hub documentation

The documentation site for [hub.formbricks.com](https://hub.formbricks.com), built with
[Astro](https://astro.build) and [Starlight](https://starlight.astro.build).

The API reference is generated from `../openapi.yaml` — the same spec the Go server is
built against — by [`starlight-openapi`](https://starlight-openapi.vercel.app/). There is
no external service in the build: `pnpm build` works offline with no API keys.

## Commands

Run these from this directory (`docs/`):

| Command                | What it does                                         |
| ---------------------- | ---------------------------------------------------- |
| `pnpm install`         | Install dependencies                                 |
| `pnpm dev`             | Dev server at http://localhost:4321                  |
| `pnpm build`           | Production build into `dist/`                        |
| `pnpm preview`         | Serve the built site locally                         |
| `pnpm check`           | `astro check` — types and content-collection schemas |
| `pnpm check:links`     | Check built internal links and assets                |
| `pnpm check:redirects` | Check built legacy API redirects                     |
| `pnpm format`          | Prettier across this directory                       |

## Layout

- `src/content/docs/` — the pages (`.md`/`.mdx`). Landing page is `index.mdx`.
- `src/content/docs/core-concepts/`, `guides/`, `reference/` — the three prose sections.
- `astro.config.ts` — site config, sidebar topics, and the OpenAPI reference wiring.
- `src/assets/` — images imported from content.
- `public/` — served as-is (favicons, web manifest).
- `theme.css` — brand overrides on top of Starlight's defaults.

## Editing

Prose pages are Markdown/MDX with Starlight frontmatter (`title`, `description`). Use
Starlight's [components](https://starlight.astro.build/components/using-components/) —
`<Aside>`, `<Card>`, `<LinkButton>`, `<Tabs>` — imported from
`@astrojs/starlight/components`.

The API reference is not hand-written. To change it, change `../openapi.yaml`.

## Cloudflare Pages deployment

`.github/workflows/docs-build.yml` checks PRs and `main` without build
secrets. Its GitHub Pages deployment stays in place until the Cloudflare
cutover is verified. Cloudflare Pages deploys through its GitHub integration.
Configure one Pages project:

| Setting                | Value                                                                  |
| ---------------------- | ---------------------------------------------------------------------- |
| Repository             | `formbricks/hub`                                                       |
| Production branch      | `main`                                                                 |
| Root directory         | `docs`                                                                 |
| Build command          | `pnpm check && pnpm build && pnpm check:links && pnpm check:redirects` |
| Build output directory | `dist`                                                                 |
| Environment variables  | `NODE_VERSION=24`, `PNPM_VERSION=12.10.1`                              |

The build reads `../openapi.yaml` from the same Git checkout. It needs no build
secrets. Keep production and pull-request deployments enabled so changes to the
spec or docs get a preview and then publish from `main`.

Before changing production DNS, verify the production `*.pages.dev` URL over HTTPS:
homepage, guides, API reference, search, assets, `/sitemap-index.xml`, and the
legacy paths in `legacy-stainless-routes.json`. The site has root-relative links,
so this URL can exercise navigation. The canonical origin remains
`https://hub.formbricks.com` throughout the preview.

The live `hub.formbricks.com` CNAME is Terraform-managed in `formbricks/infra`
and currently points to `customers.stldocs.app`. Only after the Pages preview
passes, attach `hub.formbricks.com` to the Pages project and change that CNAME
to the project's `*.pages.dev` hostname in a separate infra pull request.
Cloudflare's custom-domain setup may offer to replace the DNS record itself;
coordinate that confirmation with the Terraform change. A CNAME alone is not
enough: Pages must recognize the custom domain. Verify Pages reports the domain
active, HTTPS serves the correct certificate, and the live route checks pass.
Keep the Stainless DNS target and verification TXT records for rollback.
If needed, restore the Stainless CNAME through infra and verify the old site.

`legacy-stainless-routes.json` records the API URLs served by Stainless at migration.
Every entry must have a destination in `legacy-redirects.json`. The build generates
`dist/_redirects` from that map so Cloudflare Pages returns HTTP 301 for both
slash forms. Astro also emits HTML refresh pages for static-host fallbacks.
`pnpm check:redirects` validates the mapping, generated rules, fallback pages,
and destination pages.

Stainless also serves `/index.md` and `/quickstart/index.md` as raw Markdown.
Recreating Markdown exports across the new site is tracked in ENG-2530. The old
`/api/index.md` currently returns 404.
