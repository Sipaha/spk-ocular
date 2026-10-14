# SPK Ocular 1.6.2

- Resizing the log panel keeps visible lines at their screen positions. Follow stays at the latest line; reading history does not turn Follow back on. Shrinking, growing, cancelling a resize and restoring a hidden log tab preserve the position within the available scroll range.
- Switching Wrap on or off keeps the latest line visible while Follow is enabled, including when Wrap changes during a resize.
- New configuration unlocks local encrypted storage directly with the master password, then opens the YAML editor without requiring Connect or starting a cluster connection. This also works when a master password exists but no encrypted configurations have been saved yet, or the last one was removed.
- Cancelling password entry leaves storage unchanged. Connect still unlocks and continues only the explicitly selected connection. The explanation for creating a configuration is available in all eight application languages.
