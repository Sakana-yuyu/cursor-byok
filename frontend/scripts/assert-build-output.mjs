import { readdir, readFile } from "node:fs/promises";
import path from "node:path";

const frontendRoot = path.resolve(import.meta.dirname, "..");
const distRoot = path.join(frontendRoot, "dist");
const assetsRoot = path.join(distRoot, "assets");

function assetReferences(html) {
  const references = [];
  const pattern = /<(?:script|link)\b[^>]+(?:src|href)=["']([^"']+)["'][^>]*>/gi;
  for (const match of html.matchAll(pattern)) {
    references.push(match[1]);
  }
  return references;
}

const html = await readFile(path.join(distRoot, "index.html"), "utf8");
const eagerEditorAssets = assetReferences(html).filter((reference) => (
  path.basename(reference).toLowerCase().includes("md-editor")
));

if (eagerEditorAssets.length > 0) {
  throw new Error("entry HTML eagerly references Markdown editor assets: " + eagerEditorAssets.join(", "));
}

const assetNames = await readdir(assetsRoot);
const javascriptAssets = assetNames.filter((name) => name.endsWith(".js"));
let editorChunkFound = false;
for (const name of javascriptAssets) {
  const body = await readFile(path.join(assetsRoot, name), "utf8");
  if (body.includes("md-editor-v3") || body.includes("md-editor-modal")) {
    editorChunkFound = true;
    break;
  }
}

if (!editorChunkFound) {
  throw new Error("Markdown editor chunk was not generated; the editor feature may be unreachable");
}

console.log("build output assertion passed: Markdown editor remains lazy and reachable");
