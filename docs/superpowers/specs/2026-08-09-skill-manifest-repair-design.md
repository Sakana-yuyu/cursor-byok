# Skill Manifest Repair Design

## Problem

The Skills and MCP settings page reports two invalid skills. The bundled image reader creates `~/.cursor/skills/image-see/scripts/vision_mcp_server.py` without creating the required `SKILL.md`. Codex stores built-in skills below `~/.codex/skills/.system/<skill>/SKILL.md`, while the current scanner treats `.system` itself as a leaf skill and does not scan its children.

## Design

Bundle a valid `image-see` manifest next to the existing Python asset. `ensureVisionReaderScript` will validate and repair the manifest beside every detected installation, while preserving existing scripts. For a new installation it writes the manifest before the script, and invalid manifests are replaced with a temporary-file, backup, and rename sequence so failures do not leave a newly created incomplete skill.

Treat Codex `.system` as a known container. Add it as a Codex scan root and skip the container entry while scanning its parent. Because each scan root already fingerprints direct child manifests, adding the nested root also makes cache invalidation cover system skills without introducing recursive scanning for unrelated directories.

## Error Handling

If the image reader manifest cannot be created, enabling the reader fails with a contextual error instead of leaving another incomplete installation. Existing valid manifests are preserved. Missing scan roots remain non-errors, matching current behavior.

## Testing

- Verify a Codex `.system` child is discovered as a valid Codex skill and `.system` is absent from invalid diagnostics.
- Verify image reader deployment creates both the script and a manifest accepted by the real skill parser.
- Verify invalid manifests are replaced, every existing installation is repaired, and a manifest failure does not leave a newly deployed script behind.
- Run focused tests, all Go tests, vet, build, and `git diff --check`.
