# SPK Ocular 1.6.0

## Agent container commands

- Local agents can discover container instances and prepare/run finite commands through `GetExecInfo`, `PrepareExec` and `RunExec`. Commands return separate stdout/stderr as base64 and an explicitly observed exit status, with output and duration limits.
- A separate `exec` permission requires explicitly named resource kinds and an explicit instance/container choice. Every command requires confirmation by default; the review shows exact argv, finite stdin, the target and limits. Persistent interactive shells, implicit shell selection, attach and automatic command replay are not provided.
- Kubernetes namespace and Docker Compose project boundaries, target identity, UID and configuration-revision checks remain enforced. Grant changes cancel active transport. Remote process termination is best effort; incomplete or unknown outcomes are reported. Standalone Docker containers remain outside project grants.
- Exec uses container permissions and can read mounted secrets or change container/host data. Commands, input and output are not retained in the audit journal. Existing permissions never acquire exec automatically.

## Fixes

- Finite Docker command input delivers EOF while retaining stdout/stderr; commands without input detach stdin. Unsupported finite-input transports refuse before starting the command.
- Download deletion confirmation reliably focuses Cancel after preparation finishes.
- German agent confirmations render correctly encoded text. New exec controls and confirmations are translated into all eight interface languages.

Packages retain the unified desktop/browser executable and verification on all six native OS/architecture targets.
