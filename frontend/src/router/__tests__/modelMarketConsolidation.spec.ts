import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const currentDir = dirname(fileURLToPath(import.meta.url));
const src = resolve(currentDir, "../..");
const routerSource = readFileSync(resolve(src, "router/index.ts"), "utf8");
const appHeaderSource = readFileSync(
  resolve(src, "components/layout/AppHeader.vue"),
  "utf8",
);
const settingsSource = readFileSync(
  resolve(src, "views/admin/SettingsView.vue"),
  "utf8",
);
const settingsAPISource = readFileSync(
  resolve(src, "api/admin/settings.ts"),
  "utf8",
);
const gatewayRoutesSource = readFileSync(
  resolve(src, "../../backend/internal/server/routes/gateway.go"),
  "utf8",
);

describe("model market consolidation", () => {
  it("keeps /model-plaza canonical and redirects the colliding /models SPA path", () => {
    expect(gatewayRoutesSource).toContain('r.GET("/models"');
    expect(routerSource).toMatch(/path: ["']\/models["']/);
    expect(routerSource).toMatch(/path: ["']\/model-plaza["']/);
    expect(routerSource).toMatch(
      /redirect: \(to\) => \(\{ path: ["']\/model-plaza["'], query: to\.query \}\)/,
    );
    expect(routerSource).toMatch(
      /path: ["']\/model-plaza["'][\s\S]*?import\(["']@\/views\/public\/ModelsView\.vue["']\)/,
    );
    expect(routerSource).not.toMatch(
      /import\(["']@\/views\/ModelPlazaView\.vue["']\)/,
    );
  });

  it("uses the same route, label and feature flag in the authenticated header", () => {
    expect(appHeaderSource).toContain('v-if="user && modelMarketEnabled"');
    expect(appHeaderSource).toContain('to="/model-plaza"');
    expect(appHeaderSource).toMatch(/t\(["']modelMarket\.navLabel["']\)/);
    expect(appHeaderSource).toContain("FeatureFlags.publicModelMarket");
    expect(appHeaderSource).not.toContain("FeatureFlags.modelPlaza");
  });

  it("exposes only the canonical model market controls in admin settings", () => {
    expect(settingsSource).toContain("form.public_model_market_enabled");
    expect(settingsSource).toContain(
      "form.public_model_market_reference_usd_cny_rate",
    );
    expect(settingsSource).toContain(
      "form.public_model_market_settlement_usd_cny_rate",
    );
    expect(settingsSource).not.toContain("form.model_plaza_");
    expect(settingsAPISource).toContain("public_model_market_enabled: boolean");
  });
});
