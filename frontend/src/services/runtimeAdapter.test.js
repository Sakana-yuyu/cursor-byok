import assert from "node:assert/strict";
import test from "node:test";

const macOSUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 Safari/605.1.15";
const iPhoneUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1";

async function importRuntimeAdapter(userAgent) {
  const originalNavigator = globalThis.navigator;
  Object.defineProperty(globalThis, "navigator", {
    configurable: true,
    value: { userAgent },
  });

  try {
    return await import(`./runtimeAdapter.js?userAgent=${Date.now()}`);
  } finally {
    Object.defineProperty(globalThis, "navigator", {
      configurable: true,
      value: originalNavigator,
    });
  }
}

test("browser preview detects macOS from its user agent", async () => {
  const { runtimeIsMacOS, runtimeIsWindows } = await importRuntimeAdapter(macOSUserAgent);
  assert.equal(runtimeIsMacOS, true);
  assert.equal(runtimeIsWindows, false);
});

test("browser preview does not classify iPhone as macOS", async () => {
  const { runtimeIsMacOS, runtimeIsWindows } = await importRuntimeAdapter(iPhoneUserAgent);
  assert.equal(runtimeIsMacOS, false);
  assert.equal(runtimeIsWindows, false);
});
