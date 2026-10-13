# SPK Ocular 1.5.0

## Downloads

- A shared Downloads panel lists container file/folder copies and log exports from the main window and detached log windows. Progress, completion, failure, cancellation and interruption remain distinct; browser handoff is labelled separately from a verified save.
- Desktop entries can open the saved item, show its containing folder and copy its full path. The cross removes only the history entry; confirmed permanent deletion removes the file or folder and its contents. Deletion starts on Cancel and previews the exact path. Browser-managed files do not expose native paths or OS file actions.
- Compact rows show a file-type icon, filename and readable size. Ages younger than one minute are hidden; older relative ages are fixed when the panel opens and refresh on reopening. Hover the size or visible age for the exact date.
- A stationary toolbar icon shows a left-to-right indeterminate runner while downloads are active. New successful results highlight the button until the panel is opened. Reduced-motion preferences keep the runner static; the popup has a clear outline and consistent keyboard focus.
- Failed entries show localized safe reasons without retaining raw errors, remote stderr or log contents. The first failure survives cleanup; older or unclassified failures explicitly report that the reason is unavailable.
- History retains the latest 200 metadata entries across restarts, preserving active transfers. Interrupted work is not retried automatically. Clear history and per-entry dismissal never delete files; disabled Clear history stays muted on hover.

All eight interface languages remain supported. Existing connection, edit, Secret and agent-permission guards remain intact. Packages retain the unified desktop/browser executable and verification on all six native OS/architecture targets.
