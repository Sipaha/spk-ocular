# SPK Ocular 1.9.0

- Resource lists, Helm, Graph, Timeline, Security and RBAC share consistent toolbars and retained-content loading, progress, cancellation and retry controls. Helm sections now live in the left navigation after Storage.
- Pod properties separate regular, init and debug containers, retain sidecar labels, and wrap long names and images clearly.
- Calendar dates use local time, a 24-hour clock and dd.mm.yyyy across interface languages, with the original ISO timestamp on hover.
- Scale deployments directly from their properties. Action reviews show the connection display name; minus/plus controls adjust replicas within the allowed range before Review.
- Selecting a comparison baseline no longer shifts the workspace. Text differences include surrounding context and bounded paging.
- New container-exec permissions apply to the selected namespace/project without a resource-kind picker. Existing restricted grants and confirmation defaults are preserved; permission checkboxes have clearer active and disabled states.
- Logs identify their connection and resource, use All time terminology, and place filtering before local search. Backend filtering progressively scans bounded recent history; partial matches appear during loading while new live records are captured concurrently. Limits and incomplete results remain explicit.
- Downloads open automatically and save to the system Downloads folder without prompting. Right-click log download to choose a destination. Repeated names receive a suffix; active jobs can be cancelled from Downloads, including jobs owned by detached log windows.
- Click a completed download row to open it. Successful file opening or Show in folder closes the popup. Popup positioning, row heights and log-footer geometry remain stable during progress changes.
- Connection progress no longer shifts its provider/title area. YAML opens without a plain-text flash, preloads on intent and reuses the editor when switching resources; editable drafts retain their existing protections.
