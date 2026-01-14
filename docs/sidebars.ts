import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

// This runs in Node.js - Don't use client-side code here (browser APIs, JSX...)

/**
 * Creating a sidebar enables you to:
 - create an ordered group of docs
 - render a sidebar for each doc of that group
 - provide next/previous navigation

 The sidebars can be generated from the filesystem, or explicitly defined here.

 Create as many sidebars as you want.
 */
const sidebars: SidebarsConfig = {
  tutorialSidebar: [
    "index",
    "quickstart",
    {
      type: "category",
      label: "Core Concepts",
      items: [
        "core-concepts/data-model",
        "core-concepts/authentication",
        "core-concepts/webhooks",
        "core-concepts/connectors",
        "core-concepts/ai-enrichment",
        "core-concepts/semantic-search",
      ],
    },
    "api-reference",
    {
      type: "category",
      label: "Reference",
      items: ["reference/environment-variables", "reference/architecture"],
    },
  ],
};

export default sidebars;
