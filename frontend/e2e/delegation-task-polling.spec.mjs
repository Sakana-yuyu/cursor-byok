import { expect, test } from "@playwright/test";
import { basePreviewConfig, seedPreviewTestPlan } from "./helpers.mjs";

const CALLS_KEY = "cursor-byok.browser-preview.calls";

function taskSnapshotCalls(page) {
  return page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "[]")
    .filter((item) => item.name === "GetDelegationTaskSnapshots"), CALLS_KEY);
}

test("首页空闲时在旧轮询周期内不重复查询委派任务快照", async ({ page }) => {
  await seedPreviewTestPlan(page, { recordCalls: true, delegationTasks: [] }, basePreviewConfig());
  await page.goto("/");

  await expect.poll(() => taskSnapshotCalls(page)).toHaveLength(1);
  await page.waitForTimeout(2200);
  await expect(taskSnapshotCalls(page)).resolves.toHaveLength(1);
});
