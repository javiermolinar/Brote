# Debug Handover for VS Code

A local companion for the Debug Handover Go / Delve broker. Connect to the same paused process that Codex is inspecting, step in VS Code, then return control without restarting.

Install this VSIX and open the source project as a trusted local workspace. Run `debug-handover handover ID --editor vscode` from the broker CLI, or select VS Code in the live inspector. The extension attaches automatically using VS Code's debugger API.

Commands:

- **Debug Handover: Give Control to Codex** — return a settled pause and notify the bound task.
- **Debug Handover: Attach Pending Session** — retry after a failure or deliberate disconnect.
- **Debug Handover: Open Live Inspector** — open the shared browser panel.

The status bar also offers Give Control to Codex. Closing the frontend preserves the target. End the target explicitly through the broker's End Session action.

Only exact trusted local workspace folders are matched. Set Debug Handover: Session Directory for a custom DEBUG_HANDOVER_HOME. Remote workspaces and arbitrary process attachment are outside this prototype. See the broker README in the source package for launch, recovery, and protocol details.

This extension is not published to the Marketplace. It is not an official Microsoft or OpenAI integration.
