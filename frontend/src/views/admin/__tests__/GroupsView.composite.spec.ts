import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const currentDir = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(resolve(currentDir, "../GroupsView.vue"), "utf8");

describe("GroupsView composite group integration", () => {
  it("keeps composite creation, filtering, routing and cross-platform account copy wired", () => {
    expect(source.match(/\.\.\.GROUP_PLATFORM_OPTIONS/g)).toHaveLength(2);
    expect(source).toContain('data-testid="group-composite-routes"');
    expect(source).toContain("<CompositeRoutesModal");
    expect(source).toContain(
      'targetPlatform === "composite" || sourcePlatform === targetPlatform',
    );
    const modelsListGuard = source.slice(
      source.indexOf("const canConfigureModelsList"),
      source.indexOf("const loadModelsListCandidates"),
    );
    expect(modelsListGuard).toContain('GROUP_PLATFORM_OPTIONS.some');
  });
});
