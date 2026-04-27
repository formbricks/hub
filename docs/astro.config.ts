import { defineConfig } from "astro/config";
import { generateAPIReferenceItems, stainlessDocs } from "@stainless-api/docs";
import aiChat from "@stainless-api/docs-ai-chat/plugin";

// https://astro.build/config
export default defineConfig({
  integrations: [
    stainlessDocs({
      apiReference: {
        stainlessProject: "hub",
        propertySettings: {
          collapseDescription: false,
          expandDepth: 2,
        },
      },
      title: "Formbricks Hub",
      customCss: ["./theme.css"],
      header: {
        layout: "stacked",
        links: [
          {
            label: "Get started",
            link: "/",
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
              ],
            },
            {
              label: "Guides",
              autogenerate: { directory: "guides" },
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
