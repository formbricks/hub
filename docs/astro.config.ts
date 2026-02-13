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
      title: "hub",
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
              label: "Getting started",
              items: [""], // "" links to the index page
            },
            {
              label: "Guides",
              autogenerate: { directory: "guides" },
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
