import { defineConfig, envField } from "astro/config";
import { generateAPIReferenceItems, stainlessDocs } from "@stainless-api/docs";
import aiChat from "@stainless-api/docs-ai-chat/plugin";
import { posthog } from "./src/integrations/posthog";

// https://astro.build/config
export default defineConfig({
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
    stainlessDocs({
      apiReference: {
        stainlessProject: "hub",
        highlighting: {
          themes: {
            light: "github-light",
            dark: "github-dark",
          },
        },
        propertySettings: {
          collapseDescription: false,
          expandDepth: 2,
        },
      },
      title: "Formbricks Hub",
      expressiveCode: {
        themes: ["github-light", "github-dark"],
      },
      favicon: "/favicon.svg",
      logo: {
        light: "./src/assets/formbricks-hub-logo-light.svg",
        dark: "./src/assets/formbricks-hub-logo-dark.svg",
        alt: "Formbricks Hub",
        replacesTitle: true,
      },
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
      customCss: ["./theme.css"],
      header: {
        layout: "stacked",
        links: [
          {
            label: "Support",
            link: "https://github.com/formbricks/hub/discussions",
            variant: "outline",
            attrs: {
              target: "_blank",
              rel: "noreferrer",
            },
          },
        ],
      },
      experimental: {
        aiChat: aiChat(),
      },
      tabs: [
        {
          label: "Guides",
          link: "/",
          sidebar: [
            {
              label: "Introduction",
              slug: "",
            },
            {
              label: "Quick Start Guide",
              slug: "quickstart",
            },
            {
              label: "Core concepts",
              items: [
                {
                  label: "Data Model",
                  slug: "core-concepts/data-model",
                },
                {
                  label: "Authentication",
                  slug: "core-concepts/authentication",
                },
                {
                  label: "Webhooks",
                  slug: "core-concepts/webhooks",
                },
                {
                  label: "Tenant Settings",
                  slug: "core-concepts/tenant-settings",
                },
                {
                  label: "Translated Feedback",
                  slug: "core-concepts/translated-feedback",
                },
                {
                  label: "Sentiment & Emotions",
                  slug: "core-concepts/sentiment-and-emotions",
                },
              ],
            },
            {
              label: "Guides",
              items: [
                {
                  label: "Connect Hub to AI Clients via MCP",
                  slug: "guides/hub-mcp",
                },
                {
                  label: "Connecting Hub to Power BI",
                  slug: "guides/hub-powerbi",
                },
                {
                  label: "Connecting Hub to Superset",
                  slug: "guides/hub-superset",
                },
                {
                  label: "Connecting Hub to Databricks",
                  slug: "guides/hub-databricks",
                },
                {
                  label: "Connecting Hub to Airbyte",
                  slug: "guides/hub-airbyte",
                },
                {
                  label: "Self-Hosted Embeddings",
                  slug: "guides/hub-self-hosted-embeddings",
                },
              ],
            },
            {
              label: "Reference",
              items: [
                {
                  label: "Environment Variables",
                  slug: "reference/environment-variables",
                },
              ],
            },
          ],
        },
        {
          label: "API Reference",
          link: "/api",
          sidebar: generateAPIReferenceItems({
            excludeResourceOverviewPages: true,
          }),
        },
      ],
    }),
  ],
});
