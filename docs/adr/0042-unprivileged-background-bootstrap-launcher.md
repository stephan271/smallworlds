---
status: accepted
---

# Run the Bootstrap Launcher as an unprivileged background process

The Bootstrap Launcher will enforce one per-user process, reconnect and reopen the browser when launched again, and continue Workflow Runs after the browser closes. It may stop after an idle period or an explicit quit only when no mutation is active; stopping during active work requires an explicit cancellation decision. The first release will neither install a privileged system service nor start automatically with the operating system, keeping elevation limited to the individual actions that require it.
