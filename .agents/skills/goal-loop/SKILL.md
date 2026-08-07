---
name: goal-loop
description: Use when a user message begins with `/goal <目标>` or `/goal --strict <目标>` and the task must be driven to a verified completion instead of stopping after one response.
---

# Goal Loop

`/goal <目标>` turns the current request into a completion-oriented workflow. Treat `<目标>` as the only task objective; do not restate the command as ordinary conversation.

## Command forms

| Input | Meaning |
| --- | --- |
| `/goal <目标>` | Continue autonomously until the goal is met or a real blocker is reached. |
| `/goal --strict <目标>` | Same workflow, but do not claim completion unless every stated verification succeeds. |

An empty `/goal` is invalid. Ask for the target only in that case.

## Execution contract

1. Establish a narrow, observable success condition before changing anything.
2. For work with three or more substantive steps, create a task list. Keep at most one task in progress.
3. Investigate first, then make the smallest responsible change. Preserve unrelated user changes.
4. After every substantive stage, report one line: `进度：<当前状态>`.
5. A tool call ending is not completion. Re-check the success condition and continue when it is unmet.
6. On a failed command or test, identify the cause and retry using the smallest different approach. Do not silently skip failure.
7. Ask the user only for a genuine external decision, unavailable credential, or irreconcilable requirement.

## Completion gate

Before declaring success, verify the requested behavior with the relevant tests, build, lint, runtime observation, or direct evidence. If verification is unavailable, explain the exact constraint and do not claim completion.

Only after all requirements are satisfied, emit this exact standalone line:

```text
[goal:complete]
```

Then give a concise report of changed behavior and verification results. Never emit the marker for partial progress, a proposed plan, or an unverified implementation.