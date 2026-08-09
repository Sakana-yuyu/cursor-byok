# History Explorer And Sparse Skill Copy Design

## Goal

Improve the Settings experience in three related user-facing areas: let the History and Logs panel use the available window height, present sessions in a Windows File Explorer-style details view, and explain that enabled skills are sparse-activation candidates rather than always-injected prompt content.

## History Panel Height

The History and Logs category should consume the remaining height below the Settings page header. The category panel, card, and history component must participate in one `min-height: 0` flex chain. The history list owns vertical scrolling; the Settings content scroller should not create a second competing scroll area while this category is active. A practical minimum height remains for short/mobile viewports so controls do not collapse into each other.

## Explorer Views

The history browser supports both Windows File Explorer-inspired views. A segmented icon control in the batch toolbar switches between Icon view and Details view, and the last choice is stored locally. Icon view is the default for users without a stored preference. Both views consume the same grouped history data and the same selected-session set, so switching views never clears selection.

### Icon View

Icon view navigates one directory level at a time: root years, year months, month days, and day sessions. It includes an Up button and clickable breadcrumb path. Folder items use large yellow folder icons and flow in a responsive two-column desktop layout similar to the supplied Windows reference, collapsing to one column when space is narrow. Double-clicking a folder enters it. Session items use document/conversation icons; a single click selects the session, and double-clicking a session with debug data opens Diagnostics.

### Details View

Keep the existing year, month, and day hierarchy because it communicates chronology well, and render each session as a row in a details table. A sticky header labels the columns. The primary columns are Name, Status, Modified, Type, Debug Log, and Size. Selection uses a leading checkbox and a full-row selected state. Session IDs and diagnostic actions remain available inside the name/debug columns without adding a separate action-card layer.

The details grid has stable column tracks on desktop. On narrow viewports, the grid keeps a minimum width and the list viewport scrolls horizontally, matching File Explorer behavior instead of squeezing labels until they overlap. Year/month/day rows span every column and retain expand/collapse controls and item counts.

## Skills Sparse Activation Copy

The Skills notice describes three distinct states:

1. Skills are disabled by default.
2. Enabling a skill adds it to the BYOK candidate pool; it does not inject every enabled skill into every request.
3. The runtime sparsely activates and injects only a small relevant subset for the current task. Cursor-provided skills remain outside this BYOK toggle's control.

The UI copy intentionally avoids a fixed Top-K number so it remains accurate if activation limits change.

## Testing

Playwright coverage verifies that the History category fills the available content region, defaults to Icon view, drills through folder levels, preserves selection while switching views, persists the selected view mode, exposes the Details-view column headers, and contains narrow-screen overflow within the list. The existing frontend source contract test verifies the exact sparse-activation explanation. Unit tests, lint, production build, and a live browser visual check complete verification.
