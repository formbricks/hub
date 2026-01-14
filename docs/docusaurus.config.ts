import { themes as prismThemes } from "prism-react-renderer";
import type { Config } from "@docusaurus/types";
import type * as Preset from "@docusaurus/preset-classic";
import webpack from "webpack";

// This runs in Node.js - Don't use client-side code here (browser APIs, JSX...)

const config: Config = {
  title: "Formbricks Hub Documentation",
  tagline: "Unified experience data repository for customer feedback",
  favicon: "img/favicon.ico",

  // Future flags, see https://docusaurus.io/docs/api/docusaurus-config#future
  future: {
    v4: true, // Improve compatibility with the upcoming Docusaurus v4
  },

  // Set the production url of your site here
  url: "https://hub.formbricks.com",
  // Set the /<baseUrl>/ pathname under which your site is served
  // For GitHub pages deployment, it is often '/<projectName>/'
  baseUrl: "/",

  // GitHub pages deployment config.
  // If you aren't using GitHub pages, you don't need these.
  organizationName: "formbricks", // Usually your GitHub org/user name.
  projectName: "hub", // Usually your repo name.

  onBrokenLinks: "throw",
  onBrokenMarkdownLinks: "warn",

  // Even if you don't use internationalization, you can use this field to set
  // useful metadata like html lang. For example, if your site is Chinese, you
  // may want to replace "en" with "zh-Hans".
  i18n: {
    defaultLocale: "en",
    locales: ["en"],
  },

  presets: [
    [
      "classic",
      {
        docs: {
          sidebarPath: "./sidebars.ts",
          routeBasePath: "/",
          editUrl: "https://github.com/formbricks/hub/tree/main/docs/",
        },
        blog: false,
        theme: {
          customCss: "./src/css/custom.css",
        },
      } satisfies Preset.Options,
    ],
  ],

  plugins: [
    // Temporarily disabled - plugin not generating docs properly
    // Use static Swagger UI instead
    function webpackPolyfillPlugin() {
      return {
        name: "webpack-polyfill-plugin",
        configureWebpack() {
          return {
            resolve: {
              fallback: {
                stream: require.resolve("stream-browserify"),
                buffer: require.resolve("buffer/"),
              },
            },
            plugins: [
              new webpack.ProvidePlugin({
                Buffer: ["buffer", "Buffer"],
              }),
            ],
          };
        },
      };
    },
  ],

  // Temporarily disabled
  // themes: ['docusaurus-theme-openapi-docs'],

  themeConfig: {
    image: "img/logo.svg",
    navbar: {
      title: "Formbricks Hub",
      logo: {
        alt: "Formbricks Logo",
        src: "img/logo.svg",
      },
      items: [
        {
          type: "docSidebar",
          sidebarId: "tutorialSidebar",
          position: "left",
          label: "Docs",
        },
        {
          to: "/api-reference",
          label: "API Reference",
          position: "left",
        },
        {
          href: "https://github.com/formbricks/hub",
          label: "GitHub",
          position: "right",
        },
      ],
    },
    footer: {
      style: "dark",
      links: [
        {
          title: "Docs",
          items: [
            {
              label: "Getting Started",
              to: "/",
            },
            {
              label: "API Reference",
              to: "/api-reference",
            },
          ],
        },
        {
          title: "Community",
          items: [
            {
              label: "GitHub Discussions",
              href: "https://github.com/formbricks/hub/discussions",
            },
            {
              label: "X",
              href: "https://x.com/formbricks",
            },
          ],
        },
        {
          title: "More",
          items: [
            {
              label: "GitHub",
              href: "https://github.com/formbricks/hub",
            },
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} Formbricks GmbH. Licensed under Apache-2.0. Built with Docusaurus.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
