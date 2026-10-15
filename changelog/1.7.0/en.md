# SPK Ocular 1.7.0

- Kubernetes resource lists load the complete requested kind and selected namespaces through compact shared caches, automatic small pages and live watches. Session-wide read budgets and bounded idle retention reduce unnecessary API traffic and memory retention.
- Large All Namespaces reads stop with a request to choose specific namespaces, without presenting a partial list as complete.
- Consistent refresh icons re-read lists and spin until completion. Loading shows elapsed time and received inventory payload bytes when available. Cancel replaces the list with a persistent cancelled-request placeholder; Retry or a new connection starts it again.
- The cluster graph loads only after an explicit click for each connection. Selection, search and opening details preserve the camera.
- Connection navigation, resource-kind labels, table tooltips, scoped command-palette navigation and log selection have been refined. Re-rendering selected-resource ports no longer repeats the same information request.
- Local inventory and memory measurements, raw samples, methodology and their limits are documented in `docs/performance.md`; they are not universal production-performance guarantees.
- Resource lists avoid repeated JSON decoding, reducing CPU work while loading large Kubernetes inventories.
