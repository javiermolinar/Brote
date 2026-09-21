# AgentDebugger for VS Code

Use VS Code's native debugger and Chat with the same persistent AgentDebugger session as the browser inspector.

## Start from the normal VS Code debugger

1. Press **F5** with your existing debugger configuration and stop at a breakpoint.
2. Select a source line, right-click, and choose **Ask AgentDebugger About Selection**.
3. Type in the inline comment box beside the selected line and click **Ask**. Choose a VS Code model once; the answer streams directly into that thread without opening Chat. Use **Discard question** to close an unused draft.

The pause is captured when the inline composer opens. Stepping afterwards does not silently replace that evidence.

This mode reads the selected stack frame, non-expensive scopes, and bounded variable values from VS Code's active debug adapter. It does not need the AgentDebugger CLI, broker, or a separate Delve process. Replies appear in native source comments. Follow-ups remain in the same thread and include prior turns. Each follow-up captures the current pause, or labels the saved evidence as historical when the debugger has ended. Use **Stop generating** to cancel an answer. Questions, captured context, and answers persist in VS Code workspace storage and remain historical after the process ends. Failed or cancelled answers show a retryable status.

The `#agentdebugger` inspection tool also prefers the active native session. Use the normal debugger controls to step, continue, or edit breakpoints in this mode. Native-only discussions are local to VS Code; they are not mirrored to the browser. Scope capture depends on the debug adapter's DAP support.

## Start from an AgentDebugger core session

1. Install the AgentDebugger CLI/core and this VSIX. Open your project's folder in a trusted workspace.
2. In VS Code Chat, select a model supporting tools and ask:
   `@agentdebugger Launch the precompiled binary /absolute/path/demo for project /absolute/path/project. Set a breakpoint at /absolute/path/main.go:17 with condition attempt == 3, then continue.`
3. At the breakpoint, select code and choose **Ask AgentDebugger About Selection** from the editor context menu. Enter your question.
4. The extension creates a persisted native comment thread and submits an `@agentdebugger /answer SESSION THREAD` request in Chat using the selected model. The reply appears in Chat, the native comment thread, and the browser inspector.
5. Use the comment thread's **Ask Follow-up**, **Answer in VS Code Chat**, and **Resolve Discussion** actions.

The `#agentdebugger` tool is also available to VS Code's built-in agent. It supports listing sessions, launch, attachment, stack/locals inspection, read-only evaluation, conditional breakpoints, and single execution commands. Inline `/answer` requests do not receive execution tools.

**AgentDebugger: Attach Session** connects an existing paused session without the old control-handover ceremony. Disconnecting the editor leaves the target alive. Session discovery is limited to projects inside the open workspace folders.

## Behavior and current limits

- Launch requires an existing Go executable with debug information. It never compiles implicitly.
- Execution tools request confirmation through VS Code and use scoped core task grants. A command waits at most 30 seconds for a stop; timeout or cancellation cancels the task and requests a pause.
- Asking through VS Code binds that session's questions to VS Code Chat. It does not broadcast to Pi. Finish any active agent investigation before switching bindings.
- Source-line questions capture the selected native stack frame when available. Selected text is not automatically evaluated.
- Discussions synchronize with the broker, including browser-created threads. Answering an existing question requires it to be bound to this VS Code workspace; another agent's questions are not silently taken over.
- Chat opening currently uses VS Code's workbench chat command, with a clipboard fallback if unavailable. Clicking Ask submits the question directly; no second Enter is needed.
- A model error or cancellation is shown in Chat. The question can be retried with `/answer`; the broker's delivery status may remain Thinking until that retry succeeds.
- Authenticated model-provider behavior needs a manual end-to-end check. Automated tests cover transport, persisted replies, obsolete bindings, task-scoped execution, and real VS Code DAP attachment.

Set `debugHandover.executable` if the CLI is not at `~/.local/bin/agentdebugger`. Set `debugHandover.sessionDirectory` only when using a custom runtime cache.
