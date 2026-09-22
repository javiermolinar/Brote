<p align="center"><img src="../../assets/brote-plant.png" width="120" alt="Brote, your bug-catching debugger companion"></p>

# Brote for VS Code

**Your LLM debugger companion.**

Use VS Code's native debugger and Chat with the same persistent Brote session as the browser inspector.

## Start from the normal VS Code debugger

1. Press **F5** with your existing debugger configuration and stop at a breakpoint.
2. Select a source line, right-click, and choose **Ask Brote About Selection**.
3. Type in the inline comment box beside the selected line and click **Ask**. Choose a VS Code model once; the answer streams directly into that thread without opening Chat. Use **Discard question** to close an unused draft.

The pause is captured when the inline composer opens. Stepping afterwards does not silently replace that evidence.

This mode reads the selected stack frame, non-expensive scopes, and bounded variable values from VS Code's active debug adapter. It does not need the Brote CLI, broker, or a separate Delve process. Replies appear in native source comments. Follow-ups remain in the same thread and include prior turns. Each follow-up captures the current pause, or labels the saved evidence as historical when the debugger has ended. Use **Stop generating** to cancel an answer. Questions, captured context, and answers persist in VS Code workspace storage and remain historical after the process ends. Failed or cancelled answers show a retryable status.

The `#brote` inspection tool also prefers the active native session. Use the normal debugger controls to step, continue, or edit breakpoints in this mode. Native-only discussions are local to VS Code; they are not mirrored to the browser. Scope capture depends on the debug adapter's DAP support.

## Start from an Brote core session

1. Install this extension for your platform. Its native core and browser are bundled. Install compatible Delve for Go targets, and open your project in a trusted workspace.
2. In VS Code Chat, select a model supporting tools and ask:
   `@brote Launch the precompiled binary /absolute/path/demo for project /absolute/path/project. Set a breakpoint at /absolute/path/main.go:17 with condition attempt == 3, then continue.`
3. At the breakpoint, select code and choose **Ask Brote About Selection** from the editor context menu. Enter your question.
4. The extension creates a persisted native comment thread and submits an `@brote /answer SESSION THREAD` request in Chat using the selected model. The reply appears in Chat, the native comment thread, and the browser inspector.
5. Use the comment thread's **Ask Follow-up**, **Answer in VS Code Chat**, and **Resolve Discussion** actions.

The `#brote` tool is also available to VS Code's built-in agent. It supports listing sessions, launch, attachment, stack/locals inspection, read-only evaluation, conditional breakpoints, and debugging tasks. Inline `/answer` requests do not receive execution tools.

**Brote: Attach Session** connects an existing paused session without the old control-handover ceremony. Disconnecting the editor leaves the target alive. Session discovery is limited to projects inside the open workspace folders.

## Behavior and current limits

- Launch requires an existing Go executable with debug information. It never compiles implicitly.
- Asking the agent to debug authorizes execution within that request, without a second confirmation. It records the scope with `task-start`, keeps the task across steps, and uses `task-complete` when done or `task-cancel` on failure. A command waits at most 30 seconds for a stop; timeout or cancellation cancels the task and requests a pause. Attachment and state questions do not start execution tasks.
- Asking through VS Code binds that session's questions to VS Code Chat. It does not broadcast to Pi. Finish any active agent investigation before switching bindings.
- Source-line questions capture the selected native stack frame when available. Selected text is not automatically evaluated.
- Discussions synchronize with the broker, including browser-created threads. Answering an existing question requires it to be bound to this VS Code workspace; another agent's questions are not silently taken over.
- Chat opening currently uses VS Code's workbench chat command, with a clipboard fallback if unavailable. Clicking Ask submits the question directly; no second Enter is needed.
- A model error or cancellation is shown in Chat. The question can be retried with `/answer`; the broker's delivery status may remain Thinking until that retry succeeds.
- Authenticated model-provider behavior needs a manual end-to-end check. Automated tests cover transport, persisted replies, obsolete bindings, task-scoped execution, and real VS Code DAP attachment.

The shared-session runtime resolves the bundled core automatically. Set `debugHandover.executable` only to override it with an absolute compatible executable path. An outdated explicit override fails with upgrade guidance. Set `debugHandover.sessionDirectory` only when using a custom runtime cache.

Initial release targets macOS/Linux arm64/x64 and VS Code 1.138 or later. Native F5 mode uses your existing debug adapter; shared sessions use the bundled core.
