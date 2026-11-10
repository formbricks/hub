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

`.github/workflows/docs-build.yml` checks PRs and `main` without build secrets.
Its GitHub Pages deployment stays in place until the Cloudflare cutover is
verified. The separate `.github/workflows/docs-deploy-cloudflare.yml` downloads
the checked artifact and deploys it to the Direct Upload Pages project. It does
not check out or build PR code while holding the Cloudflare token.

| Setting               | Value                                                                   |
| --------------------- | ----------------------------------------------------------------------- |
| Cloudflare account    | Formbricks (`6bc0890b1bb5fa088eb89a3368d88929`)                         |
| Pages project         | `formbricks-hub-docs`                                                   |
| Production branch     | `main`                                                                  |
| GitHub Actions secret | `CLOUDFLARE_API_TOKEN`, scoped to Cloudflare Pages Edit in this account |

The build reads `../openapi.yaml` from the same Git checkout and uses Node 24
and pnpm 12.10.1. It needs no build secrets. A successful `main` build deploys
production. A successful same-repository PR build deploys a preview from the
checked artifact under a `pr-run-<workflow-run-id>` branch. Fork PRs receive
build checks but no Cloudflare deployment. The current user-owned token
`hub-docs-github-actions` expires November 10, 2027. Rotate it before then;
prefer an account-owned Pages Edit token when one can be created. Never commit
the token or add it to the build job.

Before changing production DNS, verify the production `*.pages.dev` URL over HTTPS:
homepage, guides, API reference, search, assets, `/sitemap-index.xml`, and the
legacy paths in `legacy-stainless-routes.json`. The site has root-relative links,
so this URL can exercise navigation. The canonical origin remains
`https://hub.formbricks.com` throughout the preview.

This cutover accepts one known content gap: Stainless serves `/index.md` and
`/quickstart/index.md` as raw Markdown today, but the Pages build will return
404 for both. Confirm that behavior in the preview and record it in the cutover
decision; these two URLs are accepted exceptions to this migration. Follow-up
[ENG-2530](https://linear.app/formbricks/issue/ENG-2530/hub-docs-replace-the-ai-chat-dropped-with-the-stainless-migration)
tracks whether and how to restore Markdown exports.

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
Previously cached HTTP 301 responses can continue sending clients from legacy
URLs to Pages destinations after a DNS rollback, so check those paths separately.

`legacy-stainless-routes.json` records the API URLs served by Stainless at migration.
Every entry must have a destination in `legacy-redirects.json`. The build generates
`dist/_redirects` from that map so Cloudflare Pages returns HTTP 301 for both
slash forms. Astro also emits HTML refresh pages for static-host fallbacks.
`pnpm check:redirects` validates the mapping, generated rules, fallback pages,
and destination pages.

The old `/api/index.md` currently returns 404.
