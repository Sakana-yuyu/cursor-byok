import assert from "node:assert/strict";
import test from "node:test";

const macOSUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 Safari/605.1.15";

test("browser preview detects macOS from its user agent", async () => {
  const originalNavigator = globalThis.navigator;
  Object.defineProperty(globalThis, "navigator", {
    configurable: true,
    value: { userAgent: macOSUserAgent },
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
