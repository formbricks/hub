import type { AstroIntegration } from "astro";

export const posthog: AstroIntegration = {
  name: "posthog-init",
  hooks: {
    "astro:config:setup": ({ injectScript }) => {
      injectScript("page", 'import "/src/scripts/posthog-init";');
    },
  },
};
