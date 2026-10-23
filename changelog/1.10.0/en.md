# SPK Ocular 1.10.0

- Connect local agents through the in-process MCP server, with explicit grants and reviewed operations. Codex and Claude Code setup is available on request, with manual configuration for other clients. Agent access replaces the previous local protocol with MCP Streamable HTTP; existing clients must be reconfigured.
- Inspect native Argo CD and Flux views, application/controller state and resource links. Edit and scale reviews show available GitOps ownership context.
- Investigate workloads, Service/Ingress network paths and Helm releases through bounded, explicit diagnostic reads with visible gaps and source identity.
- Add label columns and saved resource-table views. Inspect cert-manager Certificate state and renewal evidence without reading Secret contents.
- Read explicitly selected Prometheus metrics history and existing VPA recommendations; review contextual workload configuration and reliability checks.
- Inspect existing namespaced Trivy configuration/RBAC and policy reports. Cluster-wide Trivy/policy reports and a selected Gatekeeper constraint audit require separate opt-in; missing evidence remains unknown.
- Import KRR reports into backend-owned temporary storage, or review a temporary Kubernetes Job before running KRR. Inspect a selected Node's namespace resource declarations without presenting them as measured spare capacity.
- Import bounded offline Polaris JSON audit reports locally, without installing a scanner or querying a cluster for the import.
- Compose logs from explicitly selected workloads with overlap handling, source attribution, filtering and finite export.
- Open reviewed Node Shell and Files helpers with shared lifecycle and durable cleanup reporting. Files supports deletion and selecting revealed items; Files and Shell can detach or return with Alt+Shift+Enter.
- Learn Kubernetes through offline help and an isolated task-based introductory path, with clearer diagnostic feedback and all eight interface languages.
- Pod pickers explain rollout revision and readiness; comparison guidance is clearer. Forwarded connections retain reconnect state when old upstreams retire.
- Release builds use Go 1.26.10, including its certificate trust security fix.
