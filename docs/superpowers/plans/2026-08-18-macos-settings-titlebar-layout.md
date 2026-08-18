# macOS Settings Titlebar Layout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the settings workspace discoverable and usable in the macOS desktop app without replacing the native macOS traffic-light window controls.

**Architecture:** Keep one app-level settings navigation handler in `MainLayout.vue`, but split its rendering from Windows caption buttons. Introduce a small platform abstraction that identifies macOS alongside Windows, then give macOS a standalone right-side settings control and balanced title layout. Extend browser-preview Playwright coverage with an explicit macOS user-agent scenario so the platform-specific DOM and narrow viewport layout cannot regress.

**Tech Stack:** Vue 3 `<script setup>`, Vue Router, Tailwind CSS utility classes, Vite browser-preview mode, Playwright.

## Global Constraints

- macOS must retain its native close, minimize, and maximize traffic-light controls; do not render the custom Windows caption controls there.
- The settings control must remain a non-draggable target inside the draggable titlebar.
- At the existing narrow responsive boundary (`<640px`), the header must not introduce horizontal document or body overflow.
- Browser preview remains platform-neutral by default; tests may override the user agent before loading the app to emulate macOS.
- Do not change the release version or create/push a release tag until the user supplies the exact target version.
- Do not alter unrelated untracked specifications in the primary checkout.

---

## File Structure

- Modify: `frontend/src/services/runtimeAdapter.js` — expose a runtime macOS platform boolean derived from the existing preview/user-agent detection boundary.
- Create: `frontend/src/utils/isMacOS.js` — provide a reactive platform flag following the existing `isWindows.js` convention.
- Modify: `frontend/src/layouts/MainLayout.vue` — separate settings navigation from Windows-only caption controls and reserve the appropriate titlebar space per platform.
- Modify: `frontend/e2e/routes-smoke.spec.mjs` — add an explicit macOS titlebar regression test that checks visibility, click navigation, absent Windows caption buttons, in-viewport geometry, and horizontal-overflow safety.

## Task 1: Add a Testable macOS Platform Flag

**Files:**
- Modify: `frontend/src/services/runtimeAdapter.js:42-46`
- Create: `frontend/src/utils/isMacOS.js`
- Test: `frontend/src/services/runtimeAdapter.test.js`

**Interfaces:**
- Consumes: `isBrowserPreview` and `navigator.userAgent` from `runtimeAdapter.js`.
- Produces: `runtimeIsMacOS: boolean` from `runtimeAdapter.js`; `isMacOS: Ref<boolean>` from `utils/isMacOS.js`.

- [ ] **Step 1: Write the failing unit test**

Add a node-test case that dynamically imports `runtimeAdapter.js` with a macOS-like `navigator.userAgent`, asserts `runtimeIsWindows === false` and `runtimeIsMacOS === true`, and restores `navigator` after the assertion. Use the same module-cache-busting import URL pattern already used in `runtimeAdapter.test.js`.

```js
test("detects macOS runtime from a macOS user agent", async () => {
  const originalNavigator = globalThis.navigator;
  Object.defineProperty(globalThis, "navigator", {
    configurable: true,
    value: { userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)" },
  });

  try {
    const { runtimeIsMacOS, runtimeIsWindows } = await import(`./runtimeAdapter.js?mac=${Date.now()}`);
    assert.equal(runtimeIsMacOS, true);
    assert.equal(runtimeIsWindows, false);
  } finally {
    Object.defineProperty(globalThis, "navigator", {
      configurable: true,
      value: originalNavigator,
    });
  }
});
```

- [ ] **Step 2: Run the focused test to verify it fails**

Run: `cd frontend && node --test src/services/runtimeAdapter.test.js`

Expected: FAIL because `runtimeIsMacOS` is not exported.

- [ ] **Step 3: Implement the minimal platform export and reactive helper**

In `runtimeAdapter.js`, export the macOS predicate next to `runtimeIsWindows`:

```js
export const runtimeIsMacOS = isBrowserPreview
  ? false
  : typeof navigator !== "undefined" && /Macintosh|Mac OS X/i.test(navigator.userAgent);
```

Create `frontend/src/utils/isMacOS.js` with the established reactive wrapper:

```js
import { ref } from "vue";
import { isBrowserPreview, runtimeIsMacOS } from "@/services/runtimeAdapter";

export const isMacOS = ref(isBrowserPreview ? false : runtimeIsMacOS);
```

- [ ] **Step 4: Run the unit-test suite to verify it passes**

Run: `cd frontend && yarn test:unit`

Expected: PASS, including the macOS platform-detection case.

- [ ] **Step 5: Commit the platform abstraction**

```bash
git add frontend/src/services/runtimeAdapter.js frontend/src/services/runtimeAdapter.test.js frontend/src/utils/isMacOS.js
git commit -m "feat(frontend): detect macOS runtime"
```

### Task 2: Render a macOS-Safe Settings Entry Point

**Files:**
- Modify: `frontend/src/layouts/MainLayout.vue:1-20, 213-286`
- Test: `frontend/e2e/routes-smoke.spec.mjs`

**Interfaces:**
- Consumes: `isMacOS: Ref<boolean>` from `@/utils/isMacOS` and the existing `openSettingsWorkspace(): void` handler.
- Produces: a standalone macOS settings button with `aria-label="打开设置"`; Windows-only caption buttons retain their existing `aria-label` values.

- [ ] **Step 1: Write the failing macOS titlebar regression test**

Add a `test.describe("macOS 标题栏", ...)` block that creates a browser context whose user agent contains `Macintosh`, then verifies the main route at a narrow desktop width:

```js
test("显示设置入口且不显示 Windows 窗口按钮", async ({ browser }) => {
  const context = await browser.newContext({
    userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 Safari/605.1.15",
    viewport: { width: 640, height: 800 },
  });
  const page = await context.newPage();
  await seedPreviewTestPlan(page, {}, basePreviewConfig());
  await page.goto("/");

  const settings = page.getByRole("button", { name: "打开设置" });
  await expect(settings).toBeVisible();
  await expect(page.getByRole("button", { name: "最小化窗口" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "最大化窗口" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "关闭窗口" })).toHaveCount(0);

  const box = await settings.boundingBox();
  expect(box).not.toBeNull();
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(640);
  await settings.click();
  await expect(page).toHaveURL(/#?\/settings/);

  const layout = await page.evaluate(() => ({
    viewportWidth: window.innerWidth,
    documentWidth: document.documentElement.scrollWidth,
    bodyWidth: document.body.scrollWidth,
  }));
  expect(layout.documentWidth).toBeLessThanOrEqual(layout.viewportWidth + 1);
  expect(layout.bodyWidth).toBeLessThanOrEqual(layout.viewportWidth + 1);
  await context.close();
});
```

- [ ] **Step 2: Run the focused E2E test to verify it fails**

Run: `cd frontend && yarn test:e2e --grep "macOS 标题栏"`

Expected: FAIL because browser-preview currently omits the settings control outside the Windows-only group and platform detection treats all preview sessions as non-desktop.

- [ ] **Step 3: Implement platform-aware titlebar layout**

In `MainLayout.vue`:

1. Import `isMacOS` from `@/utils/isMacOS`.
2. Change the header class binding so only macOS centers its title; use a macOS-specific left inset that keeps the app title clear of native traffic lights and a right inset that reserves the standalone settings control.
3. Render the current cog button as a separate absolute control for macOS and give it a compact 28px target anchored to the right edge. Preserve `style="--wails-draggable: no-drag"`.
4. Keep the existing capsule containing the cog, minimize, maximize, and close buttons behind its current `v-if="isWindows || isBrowserPreview"`, so desktop macOS gets no custom caption controls.

Use these class contracts for the macOS-only settings control and title row:

```vue
:class="isMacOS ? '!justify-center px-[76px] pr-[52px]' : ''"
```

```vue
:class="isMacOS ? 'pr-0' : 'pr-[124px]'"
```

```vue
<button
  v-if="isMacOS"
  type="button"
  aria-label="打开设置"
  title="设置"
  class="absolute right-[14px] top-[6px] z-99999 flex h-[28px] w-[28px] items-center justify-center rounded-[7px] text-[#9a9a9a] transition-colors duration-150 hover:bg-[#3a3a3a] hover:text-[#f0f0f0]"
  :class="route.path === '/settings' ? 'cursor-default opacity-60' : 'cursor-pointer'"
  style="--wails-draggable: no-drag"
  @click="openSettingsWorkspace"
>
  <span class="icon-[mdi--cog-outline] text-[17px]" aria-hidden="true"></span>
</button>
```

- [ ] **Step 4: Run lint, focused E2E, and responsive settings E2E**

Run:

```bash
cd frontend
yarn lint
yarn test:e2e --grep "macOS 标题栏"
yarn test:e2e settings-layout.spec.mjs
```

Expected: all commands PASS. The macOS test proves the button is visible, clickable, entirely in the viewport, and that native macOS does not receive Windows caption controls. The settings-layout suite confirms no overflow regression below 640px.

- [ ] **Step 5: Commit the macOS settings layout**

```bash
git add frontend/src/layouts/MainLayout.vue frontend/e2e/routes-smoke.spec.mjs
git commit -m "fix(frontend): expose settings on macOS"
```

### Task 3: Verify the Release Candidate Without Publishing

**Files:**
- Modify: none
- Test: `frontend/e2e/routes-smoke.spec.mjs`, `frontend/e2e/settings-layout.spec.mjs`

**Interfaces:**
- Consumes: the committed platform detection and titlebar layout changes from Tasks 1 and 2.
- Produces: verified repair commit(s) ready for a versioned release once the user supplies the exact version number.

- [ ] **Step 1: Run the complete frontend verification set**

Run:

```bash
cd frontend
yarn test:unit
yarn lint
yarn test:e2e
```

Expected: PASS. If an unrelated pre-existing flaky test fails, capture its exact test name and rerun it once before deciding whether it is unrelated.

- [ ] **Step 2: Run the Go verification relevant to release builds**

Run from repository root:

```bash
go test ./...
go vet ./...
```

Expected: PASS. Report any failure with its command and output; do not claim release readiness if either command fails.

- [ ] **Step 3: Inspect the final worktree state**

Run:

```bash
git status --short
git log --oneline -3
```

Expected: only the intended committed macOS repair changes, with no version, release-notes, tag, or unrelated primary-worktree specification files included.

- [ ] **Step 4: Prepare release only after the user supplies an exact version**

Use the `uploadcursor` skill. Before editing version files, set the user-provided value exactly in both commands below:

```bash
TARGET="<user-supplied-version>"
ACTUAL="$(go run ./scripts/release version -config ./build/config.yml)"
printf 'current=%s target=%s\n' "$ACTUAL" "$TARGET"
```

Only after user confirmation of `TARGET`: update `build/config.yml`, replace `release-notes.md` with the macOS settings repair note plus the required download section, validate `ACTUAL == TARGET`, commit the release metadata, tag `v$TARGET`, and push `main` and the tag. Do not infer a version from the repository’s existing `0.0.95` value.
