import posthog from "posthog-js";
import { PUBLIC_POSTHOG_KEY, PUBLIC_POSTHOG_HOST } from "astro:env/client";

if (PUBLIC_POSTHOG_KEY) {
  posthog.init(PUBLIC_POSTHOG_KEY, {
    api_host: PUBLIC_POSTHOG_HOST,
    defaults: "2025-05-24",
    cookieless_mode: "always",
    autocapture: true,
    capture_heatmaps: false,
    capture_dead_clicks: false,
    disable_session_recording: true,
  });
  (window as any).posthog = posthog;
}
