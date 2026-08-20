# Formbricks Hub documentation

The documentation site for [hub.formbricks.com](https://hub.formbricks.com), built with
[Astro](https://astro.build) and [Starlight](https://starlight.astro.build).

The API reference is generated from `../openapi.yaml` — the same spec the Go server is
built against — by [`starlight-openapi`](https://starlight-openapi.vercel.app/). There is
no external service in the build: `pnpm build` works offline with no API keys.

## Commands

Run these from this directory (`docs/`):

| Command | What it does |
| -- | -- |
| `pnpm install` | Install dependencies |
| `pnpm dev` | Dev server at http://localhost:4321 |
| `pnpm build` | Production build into `dist/` |
| `pnpm preview` | Serve the built site locally |
| `pnpm check` | `astro check` — types and content-collection schemas |
| `pnpm format` | Prettier across this directory |

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
