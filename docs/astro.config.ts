import { defineConfig, envField } from "astro/config";
import starlight from "@astrojs/starlight";
import starlightOpenAPI, { openAPISidebarGroups } from "starlight-openapi";
import starlightSidebarTopics from "starlight-sidebar-topics";
import { posthog } from "./src/integrations/posthog";

// https://astro.build/config
export default defineConfig({
  // Canonical origin. Required by @astrojs/sitemap, which Starlight enables —
  // without it the sitemap is silently skipped. Revisit with ENG-2370 (hosting).
  site: "https://hub.formbricks.com",
  env: {
    schema: {
      PUBLIC_POSTHOG_KEY: envField.string({
        context: "client",
        access: "public",
        optional: true,
      }),
      PUBLIC_POSTHOG_HOST: envField.string({
        context: "client",
        access: "public",
        default: "https://eu.i.posthog.com",
      }),
    },
  },
  integrations: [
    posthog,
    starlight({
      title: "Formbricks Hub",
      favicon: "/favicon.svg",
      logo: {
        light: "./src/assets/formbricks-hub-logo-light.svg",
        dark: "./src/assets/formbricks-hub-logo-dark.svg",
        alt: "Formbricks Hub",
        replacesTitle: true,
      },
      expressiveCode: {
        themes: ["github-light", "github-dark"],
      },
      customCss: ["./theme.css"],
      social: [
        {
          icon: "github",
          label: "Support",
          href: "https://github.com/formbricks/hub/discussions",
        },
      ],
      head: [
        {
          tag: "link",
          attrs: {
            rel: "icon",
            type: "image/png",
            sizes: "32x32",
            href: "/favicon/favicon-32x32.png",
          },
        },
        {
          tag: "link",
          attrs: {
            rel: "icon",
            type: "image/png",
            sizes: "16x16",
            href: "/favicon/favicon-16x16.png",
          },
        },
        {
          tag: "link",
          attrs: {
            rel: "apple-touch-icon",
            sizes: "180x180",
            href: "/favicon/apple-touch-icon.png",
          },
        },
        {
          tag: "link",
          attrs: {
            rel: "manifest",
            href: "/favicon/site.webmanifest",
          },
        },
        {
          tag: "link",
          attrs: {
            rel: "mask-icon",
            href: "/favicon/safari-pinned-tab.svg",
            color: "#00c4b8",
          },
        },
        {
          tag: "meta",
          attrs: {
            name: "theme-color",
            content: "#00c4b8",
          },
        },
      ],
      plugins: [
        // Replaces the Stainless `tabs` option: one topic per former tab.
        starlightSidebarTopics([
          {
            label: "Guides",
            link: "/",
            items: [
              { label: "Introduction", slug: "" },
              { label: "Quick Start Guide", slug: "quickstart" },
              {
                label: "Core concepts",
                items: [
                  { label: "Data Model", slug: "core-concepts/data-model" },
                  { label: "Authentication", slug: "core-concepts/authentication" },
                  { label: "Webhooks", slug: "core-concepts/webhooks" },
                  { label: "Tenant Settings", slug: "core-concepts/tenant-settings" },
                  { label: "Translated Feedback", slug: "core-concepts/translated-feedback" },
                  { label: "Sentiment & Emotions", slug: "core-concepts/sentiment-and-emotions" },
                  { label: "Filtering & Sorting", slug: "core-concepts/filtering-and-sorting" },
                  { label: "Taxonomy", slug: "core-concepts/taxonomy" },
                ],
              },
              {
                label: "Guides",
                items: [
                  { label: "Connecting Hub to Power BI", slug: "guides/hub-powerbi" },
                  { label: "Connecting Hub to Superset", slug: "guides/hub-superset" },
                  { label: "Connecting Hub to Databricks", slug: "guides/hub-databricks" },
                  { label: "Connecting Hub to Airbyte", slug: "guides/hub-airbyte" },
                  { label: "Self-Hosted Embeddings", slug: "guides/hub-self-hosted-embeddings" },
                ],
              },
              {
                label: "Reference",
                items: [
                  { label: "Environment Variables", slug: "reference/environment-variables" },
                  { label: "Metrics", slug: "reference/metrics" },
                ],
              },
            ],
          },
          {
            id: "api",
            label: "API Reference",
            link: "/api/",
            items: openAPISidebarGroups,
          },
        ],
        {
          // starlight-openapi generates the per-operation and per-tag pages
          // itself, so starlight-sidebar-topics cannot attribute them to a
          // topic on its own. Claim everything under /api/ for the API
          // Reference topic; without this the build fails on the first
          // operation page it renders.
          topics: {
            api: ["/api/**"],
          },
        }),
        // Replaces Stainless's hosted `apiReference`: the reference is now generated
        // from the spec that lives beside it in this repo, with no vendor call.
        starlightOpenAPI([
          {
            base: "api",
            label: "API Reference",
            schema: "../openapi.yaml",
          },
        ]),
      ],
    }),
  ],
});
