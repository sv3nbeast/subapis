import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const currentDir = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(resolve(currentDir, "../GroupsView.vue"), "utf8");

describe("GroupsView composite group integration", () => {
  it("keeps composite creation, filtering, routing and cross-platform account copy wired", () => {
    // 官方 simple-mode 门控把创建表单的平台选项改为 GROUP_PLATFORM_OPTIONS.filter(...)，筛选器仍用展开。
    expect(source.match(/\.\.\.GROUP_PLATFORM_OPTIONS/g)).toHaveLength(1);
    expect(source).toContain("GROUP_PLATFORM_OPTIONS.filter(");
    expect(source).toContain('data-testid="group-composite-routes"');
    expect(source).toContain("<CompositeRoutesModal");
    expect(source).toContain(
      'targetPlatform === "composite" || sourcePlatform === targetPlatform',
    );
    // 官方模型白名单替代了本地的 /v1/models 自定义列表块。
    expect(source).toContain("loadModelAllowlistCandidates(");
    expect(source).not.toContain("canConfigureModelsList");
  });
});
