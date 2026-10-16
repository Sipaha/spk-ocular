# SPK Ocular 1.7.1

- Fixed outer-page scrolling and blank space below the status bar when a large logs panel is combined with a smaller window. The logs panel now fits the available height, keeping the application header and footer in place.
- Local Make builds now include a UTC build timestamp to the second and a short Git revision in the version shown in the footer, About dialog and version command. Published release versions remain unchanged.
- Fixed a Helm upgrade cancellation race that could let operation cleanup mark an already successful upgrade as failed. Active cancellation and operation deadlines remain enforced.
