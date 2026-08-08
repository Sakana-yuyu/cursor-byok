# Stats Overlay Layout Design

## Goal

Improve the expanded `engine` stats overlay so its operational metrics remain
readable at the fixed desktop-window size. The overlay must not clip its header
or chart, and the trend legend must not obscure plotted data.

## Scope

- Keep the existing cache-hit gauge, four telemetry values, chart, controls,
  palette preferences, docking behavior, and collapsed pill unchanged in
  meaning.
- Rebalance the expanded engine layout only.
- Increase the native engine window height to match the component's rendered
  content plus a small transparent-window safety margin.

## Layout

- Use a compact, right-aligned control strip with a fixed height that does not
  overlap the metrics panel.
- Give the engine telemetry panel consistent internal padding and a fixed gauge
  column, with the four values in an even two-by-two grid.
- Allocate a dedicated chart header row for the legend, leaving the plot canvas
  below it. The legend must never overlay the trend lines.
- Preserve the current green/cyan visual language and transparency behavior.
- Keep the overlay at a practical monitoring size rather than adding a new
  resize interaction.

## Behavior And Safety

- The browser preview and the native transparent window use the same calculated
  dimensions.
- Layout changes must preserve all existing action buttons, tooltip labels, and
  docking/morph interactions.
- No user-visible text is added, so locale catalogs do not change.

## Verification

1. Run the frontend production build.
2. Render the stats-overlay route at the native engine window dimensions.
3. Inspect a screenshot to confirm: no top clipping, no chart/legend overlap,
   readable telemetry values, and no horizontal overflow.
