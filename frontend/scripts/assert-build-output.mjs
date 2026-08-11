import { readdir, readFile, stat } from "node:fs/promises";
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

const eagerMarkdownParserAssets = assetReferences(html).filter((reference) => (
  path.basename(reference).toLowerCase().includes("marked")
));

if (eagerMarkdownParserAssets.length > 0) {
  throw new Error("entry HTML eagerly references Markdown parser assets: " + eagerMarkdownParserAssets.join(", "));
}

const assetNames = await readdir(assetsRoot);
const oversizedFonts = [];
for (const name of assetNames.filter((assetName) => assetName.endsWith(".ttf"))) {
  const details = await stat(path.join(assetsRoot, name));
  if (details.size > 1024 * 1024) oversizedFonts.push(`${name} (${details.size} bytes)`);
}
if (oversizedFonts.length > 0) {
  throw new Error("production assets contain oversized TTF fonts: " + oversizedFonts.join(", "));
}

const cssAssets = assetNames.filter((name) => name.endsWith(".css"));
for (const name of cssAssets) {
  const body = await readFile(path.join(assetsRoot, name), "utf8");
  if (body.includes("PingFang-Medium")) {
    throw new Error(`production CSS still references the bundled PingFang font: ${name}`);
  }
}

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

console.log("build output assertion passed: Markdown editor and parser remain lazy and reachable");
