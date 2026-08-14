# Documentation index

This repository keeps **two** markdown-heavy areas with different roles:

| Location | Role |
| --- | --- |
| [`spec/changes/`](spec/changes/) | **Active change specs** — proposals, designs, tasks, and verification notes for in-flight work. Start here for current feature intent. |
| [`docs/superpowers/`](docs/superpowers/) | **Historical / workflow plans** — brainstorming and execution plans from earlier iterations. Treat as archive unless a plan explicitly says it is still authoritative. |
| [`docs/cursor-capability-map.md`](docs/cursor-capability-map.md) | Generated capability map (run `go run ./cmd/sync-tool-catalog --write`). |
| [`release-notes.md`](release-notes.md) | User-facing release notes for shipped versions. |

When adding new documentation for a feature, prefer `spec/changes/<feature>/` with `proposal.md`, `design.md`, and `tasks.md`. Do not duplicate the same spec under `docs/superpowers/`.
