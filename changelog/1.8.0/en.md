# SPK Ocular 1.8.0

- Edit individual ConfigMap values in a syntax-highlighted editor with search and undo. Reviews show the exact text change against the current value; binary entries and Secret contents retain their existing protected presentation.
- Search resources with explicit namespace, type and name fragments. Ambiguous namespace matches offer a choice; a unique live match opens the exact object without losing unsaved edits.
- Navigation groups have consistent icons and spacing. Collapsing a group hides all its children, including the selected resource, while preserving the open page.
- Disconnected workspaces have a static provider preview, a Connect/Cancel action beside the status, and separate transparent lighthouse and mountain illustrations. The drawn lighthouse and its soft beam keep a fixed size and are clipped when the sidebar changes width.
- A single footer shows the active view's status. Refresh progress appears after two seconds without shifting toolbar controls; nested panels retain the application frame during focus and resize changes.
- Resize the focused panel with Alt+Shift+Arrow keys. Alt+Shift+Enter detaches or returns logs while keeping one stream; new detached windows use more of the current screen's working area.
- Dock menus close all tabs, or tabs to the left or right, while respecting pinned tabs and current ownership.
- Resource details use clearer typography and compact, matching revision and Events tables. Events retains sorting, column resizing and Refresh; Ingress service relations show each declared route path.
- Prevented already-delivered terminal text from being replayed during IME replacement. Active composition, Unicode paste and screen-reader announcement buffers remain protected.
