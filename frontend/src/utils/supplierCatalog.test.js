import assert from "node:assert/strict";
import test from "node:test";

import { classifyModelProtocol } from "./protocolMeta.js";
import { supplierModelCatalog, supplierTemplate } from "./supplierCatalog.js";

test("Daoxe uses its OpenAI catalog and chat completions defaults", () => {
  const template = supplierTemplate("daoxe");

  assert.equal(template.id, "daoxe");
  assert.equal(template.type, "openai");
  assert.equal(template.baseURL, "https://api.daoxe.com/v1");
  assert.equal(template.endpoint, "/v1/chat/completions");
  assert.equal(template.requestGroup, "chat_completions");
  assert.equal(
    classifyModelProtocol("openai", "gpt-oss-20b", template.baseURL, "", template.requestGroup),
    "chat_completions",
  );
  assert.deepEqual(supplierModelCatalog("daoxe"), {
    status: "openai_models",
    urls: ["https://api.daoxe.com/v1/models"],
    appendCandidates: true,
    source: "manual",
  });
});

test("unknown supplier does not fall back to Daoxe", () => {
  const template = supplierTemplate("not-daoxe");

  assert.notEqual(template.id, "daoxe");
  assert.notDeepEqual(supplierModelCatalog("not-daoxe").urls, ["https://api.daoxe.com/v1/models"]);
});
