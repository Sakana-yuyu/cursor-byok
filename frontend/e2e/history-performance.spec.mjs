import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    const sessions = [];
    let sequence = 0;
    const addSessions = (year, monthIndex, day, count) => {
      for (let index = 0; index < count; index += 1) {
        const timestamp = Date.UTC(year, monthIndex, day, 8, index, 0);
        sessions.push({
          id: `history-performance-${sequence}`,
          title: `性能历史 ${String(sequence).padStart(3, "0")}`,
          createdAtUnixMs: timestamp,
          updatedAtUnixMs: timestamp,
          sizeBytes: 1024 + sequence,
          debugSizeBytes: sequence % 7 === 0 ? 2048 : 0,
          subagentType: sequence % 2 === 0 ? "generalPurpose" : "",
          mode: "agent",
          hasDebug: sequence % 7 === 0,
          status: sequence % 11 === 0 ? "failed" : "completed",
          requestId: `request-${sequence}`,
        });
        sequence += 1;
      }
    };

    addSessions(2026, 7, 10, 120);
    addSessions(2026, 7, 9, 60);
    addSessions(2026, 6, 15, 84);
    addSessions(2026, 5, 20, 84);
    addSessions(2025, 11, 3, 78);
    addSessions(2025, 10, 2, 78);

    localStorage.setItem(
      "cursor-byok.browser-preview.test-plan",
      JSON.stringify({ historySessions: sessions }),
    );
    localStorage.setItem("cursor-byok.history.view-mode", "details");
  });
});

test("大批历史记录仅展开最新分组并按页加载详细行", async ({ page }) => {
  await page.setViewportSize({ width: 1180, height: 900 });
  await page.goto("/settings?category=history");

  await expect(page.getByText("504", { exact: true })).toBeVisible();
  const grid = page.getByRole("grid", { name: "历史详细信息列表" });
  const sessionRows = grid.locator('label[role="row"]');

  await expect(page.getByRole("button", { name: /2026.*348 项/ })).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByRole("button", { name: /2025.*156 项/ })).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByRole("button", { name: /2026年8月.*180 项/ })).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByRole("button", { name: /2026年7月.*84 项/ })).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByRole("button", { name: /2026年8月10日.*120 项/ })).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByRole("button", { name: /2026年8月9日.*60 项/ })).toHaveAttribute("aria-expanded", "false");
  await expect(sessionRows).toHaveCount(50);

  const selectedRow = sessionRows.first();
  await selectedRow.click();
  await expect(selectedRow).toHaveAttribute("aria-selected", "true");

  await page.getByRole("button", { name: "加载更多" }).click();
  await expect(sessionRows).toHaveCount(100);
  await expect(selectedRow).toHaveAttribute("aria-selected", "true");
});
