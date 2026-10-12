# SPK Ocular 1.4.0

## Search and quick tools

- Ctrl+K recovers English QWERTY queries typed with a Russian or compatible Ukrainian layout when the original query has no matches. The input remains unchanged and a localized hint shows the recovered query; scopes and resource identity are preserved.
- Opening an object from search focuses its selected table row, so resource shortcuts work immediately. Concrete Pod logs, terminals and files use the running default container, otherwise the first running container. Logs remain available for completed Pods. Multiple workload replicas still require an explicit Pod choice; right-click retains full setup.
- Opening or reopening logs focuses the log viewport. Follow shows its End shortcut in the tooltip and keyboard reference; text fields keep ordinary End behavior.

## Interface and reliability

- Modal palettes cover background scrollbars and panel dividers. Table focus includes the header and rows; popup menus remain above the dock and tooltips stay next to their buttons.
- Read-only and setup dialogs share consistent focus, Tab and close behavior. Unsaved-editor guards remain intact.
- The agent journal rejects stale replies after filter changes, retains historical targets and formats dates in the selected language. Resource relationships, Deployment revisions and Helm resource lists use compact aligned tables.
- UI HTTP requests have bounded bodies and read deadlines. Session-incarnation checks and queued inventory cancellation prevent stale reads from switching to replacement sessions. Existing permissions, scopes and provider budgets are unchanged.

## Performance

- Log regex search sends incremental batches without retaining a second history in its worker. Compose projections use local lookup indexes, and unchanged/status-only table updates avoid redundant row work.
- All eight interface languages remain supported. Packages retain the unified desktop/browser executable and the existing six-platform verification gate.
