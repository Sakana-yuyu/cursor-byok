# Skill Manifest Repair Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the false invalid-skill records for `image-see` and Codex `.system` while making both sets of real skills discoverable.

**Architecture:** Deploy an embedded manifest beside the bundled image reader script, and model Codex `.system` as an explicit secondary scan root. Keep both changes inside the existing bridge deployment and forwarder scanning boundaries.

**Tech Stack:** Go, `go:embed`, filesystem integration tests.

## Global Constraints

- Preserve all unrelated dirty worktree changes.
- Do not modify installed Cursor application bundles.
- Add failing behavioral tests before production changes.

---

### Task 1: Codex System Skill Discovery

**Files:**
- Modify: `internal/backend/forwarder/skill_multisource_test.go`
- Modify: `internal/backend/forwarder/skill_multisource.go`

**Interfaces:**
- Consumes: `orderedSkillScanRoots`, `scanAllSkillRecords`.
- Produces: Codex `.system` child skills as `SkillSourceCodex` records without a `.system` diagnostic.

- [x] **Step 1: Write the failing test**

Create a temporary Codex home with `.codex/skills/.system/imagegen/SKILL.md`, scan all records, and assert that `imagegen` is valid while `.system` is not invalid.

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/backend/forwarder -run TestCodexSystemSkillsAreDiscoveredWithoutContainerDiagnostic -count=1`

- [x] **Step 3: Write minimal implementation**

Add `.codex/skills/.system` after the ordinary Codex root and skip `.system` only when it is encountered in the parent Codex root.

- [x] **Step 4: Run test to verify it passes**

Run the focused forwarder test again.

### Task 2: Image Reader Skill Manifest Deployment

**Files:**
- Create: `internal/bridge/image_see_skill.md`
- Create: `internal/bridge/reader_mcp_test.go`
- Modify: `internal/bridge/proxy.go`

**Interfaces:**
- Consumes: `ensureVisionReaderScript`, the existing embedded Python script.
- Produces: `~/.cursor/skills/image-see/SKILL.md` and the existing script path.

- [x] **Step 1: Write the failing test**

Set a temporary home, invoke `ensureVisionReaderScript`, and assert both files exist and the manifest contains valid frontmatter.

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bridge -run TestEnsureVisionReaderScriptCreatesValidSkillManifest -count=1`

- [x] **Step 3: Write minimal implementation**

Embed the manifest and add a reusable atomic write helper that preserves existing scripts, validates and replaces damaged manifests, repairs every detected installation, and creates the manifest before a new script.

- [x] **Step 4: Run test to verify it passes**

Run the focused bridge test again.

### Task 3: Installed-State and Repository Verification

**Files:**
- Repair: `C:/Users/Administrator/.cursor/skills/image-see/SKILL.md` using the bundled manifest content.

**Interfaces:**
- Consumes: built repository behavior and real user skill directories.
- Produces: a clean current skill scan with no invalid `image-see` or `.system` records.

- [x] **Step 1: Repair current installed skill data**

Create the missing user manifest without deleting `.system`.

- [x] **Step 2: Run focused verification**

Run both new tests together and inspect the real filesystem.

- [x] **Step 3: Run full verification**

Run `go test ./... -count=1`, `go vet ./...`, `go build ./...`, and `git diff --check`.
