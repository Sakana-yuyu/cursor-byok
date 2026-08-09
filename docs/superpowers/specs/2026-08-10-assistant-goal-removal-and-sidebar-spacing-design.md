# Assistant Goal Removal And Sidebar Spacing Design

## Goal

Remove the Goal execution surface from the standalone assistant while preserving the Cursor `/goal` command, and add clearer vertical separation between sibling items in normal settings groups.

## Settings Sidebar

- Increase spacing only between sibling category buttons in the normal groups such as `服务与模型`.
- Keep the `更多设置` child list at its current compact spacing.
- Keep the collapsed desktop sidebar compact.
- Preserve all current selection, expansion, URL, and persistence behavior.

## Goal Boundary

Remove assistant-only functionality: the Home entry button, `/goal` frontend route, Goal view, browser-preview Goal mock, frontend Goal client functions, Wails panel bindings, host/client forwarding methods, and panel-only snapshots/start/stop methods in the forwarder.

Preserve Cursor functionality: Goal configuration, the Advanced Settings Goal toggle and budgets, `/goal` and `/goal --strict` parsing, Goal state attached to active Cursor streams, sparse skill activation, retries, verification, progress reporting, completion reporting, and the normal cancellation path.

## Testing

- Browser regression: Home has no Goal execution button and `/goal` does not render a Goal panel.
- Browser regression: Advanced Settings still exposes the Goal command switch.
- Layout regression: normal sibling items have a larger gap than `更多设置` sibling items.
- Go regression: existing `/goal` enabled/disabled parsing tests remain green after panel APIs are removed.

