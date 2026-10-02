import fs from "node:fs";
import path from "node:path";
import vinext from "vinext";
import { defineConfig, type Plugin } from "vite";

// macOS Seatbelt blocks FSEvents, so Codex previews need polling for HMR.
const isCodexSeatbeltSandbox = process.env.CODEX_SANDBOX === "seatbelt";

const localBindingConfig = {
  main: "./worker/index.ts",
  compatibility_flags: ["nodejs_compat"],
};

// vinext caches Google Fonts CSS in `.vinext/fonts/*/style.css` with absolute
// file paths and only rewrites paths under the current root to served URLs.
// A cache copied from another checkout would ship those foreign paths (404s),
// so drop such CSS and let vinext regenerate it for this root.
function portableFontCache(): Plugin {
  return {
    name: "centerseat:portable-font-cache",
    configResolved(config) {
      const cacheDir = path.join(config.root, ".vinext", "fonts");
      if (!fs.existsSync(cacheDir)) return;
      for (const entry of fs.readdirSync(cacheDir, { withFileTypes: true })) {
        if (!entry.isDirectory()) continue;
        const cssPath = path.join(cacheDir, entry.name, "style.css");
        if (!fs.existsSync(cssPath)) continue;
        if (!fs.readFileSync(cssPath, "utf8").includes(`url(${cacheDir}/`)) {
          fs.rmSync(cssPath);
        }
      }
    },
  };
}

export default defineConfig(async () => {
  // Keep Wrangler and Miniflare state project-local. These are non-secret tool
  // settings; application environment belongs in ignored `.env*` files.
  process.env.WRANGLER_WRITE_LOGS ??= "false";
  process.env.WRANGLER_LOG_PATH ??= ".wrangler/logs";
  process.env.MINIFLARE_REGISTRY_PATH ??= ".wrangler/registry";

  // Wrangler snapshots its log path while the Cloudflare plugin is imported.
  const { cloudflare } = await import("@cloudflare/vite-plugin");

  return {
    server: isCodexSeatbeltSandbox
      ? { watch: { useFsEvents: false, usePolling: true } }
      : undefined,
    plugins: [
      portableFontCache(),
      vinext(),
      cloudflare({
        viteEnvironment: { name: "rsc", childEnvironments: ["ssr"] },
        config: localBindingConfig,
      }),
    ],
  };
});
