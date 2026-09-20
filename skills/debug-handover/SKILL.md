---
name: debug-handover
description: Debug Go programs with Delve, then pass the same paused process between Codex and Zed or VS Code. Use for conditional breakpoints, live stack and locals inspection, precompiled executables or test binaries, and taking back a session after the user has stepped in either editor.
---

Use the bundled CLI at `../../scripts/debug-handover`, resolved from this skill directory. It builds and caches its own helper; it never compiles the user's target. Requires Go 1.23+, Delve, and the selected editor's CLI (`zed` or `code`) for opening it. Run `doctor` to diagnose versions, optional binary debug information, and Codex queue support. Go / Delve versions must be compatible. Read `../../docs/debugging.md` for workflows and prototype limits, and `../../docs/architecture.md` for component boundaries.

## Start or resume

Run `debug-handover sessions` first if a live session may already exist. Select the session matching the user's binary and project; ask only when multiple matches make their intent unclear. Inspect `state ID` before acting on an existing session. Do not launch a duplicate merely because the editor is disconnected. Offline sessions with recovery metadata can use `recover ID`, which reconnects to the same Delve process; it never starts a replacement debuggee. Use the returned panel URL if an occupied port required rebinding. Recovery fails safely for older descriptors or a dead/mismatched target.

For a new session, identify the executable, source project, working directory, and program arguments from the user's request or project configuration:

```sh
debug-handover start --binary /absolute/path/to/app --project /absolute/project -- [program arguments]
```

`--project` sets both the source project and the debuggee working directory. Use an existing binary when supplied. A test binary created with `go test -c` works the same way, passing `-- -test.run TestName`. If a build is needed, follow the user's project build instructions; a typical debug build is `go build -gcflags='all=-N -l' -o /path/to/app ./cmd/app`. Do not silently rebuild or restart a live session. Changed source requires an explicit rebuild and a new process to execute those changes.

When launched from Codex, `start` binds inspector notifications to `CODEX_THREAD_ID` after checking `codex queue` support. Use `--thread UUID` explicitly when needed or `--thread ""` for standalone debugging. To change a live session, run `bind ID --thread UUID`; bind only to the user's intended task. The start result includes a session ID and local panel URL. Open it using Codex's browser panel when available. Keep the URL private to the local user; its fragment carries the session token. The panel is the real live inspector, not an inline visualization mockup.

## Stop at a requested point

Translate the user's request into an executable source line or unambiguous function and a Delve expression. Inspect the source to choose the line; the breakpoint stops **before** the statement executes. Variables in a condition must be in scope at that location.

```sh
debug-handover break ID --file internal/worker.go --line 120 --condition 'attempt == 3'
debug-handover break ID --function main.process
debug-handover break ID --file main.go --line 40 --hit-condition '== 3'
debug-handover continue ID --wait 20s
debug-handover state ID
```

File paths resolve relative to the session project. Do not promise a condition fired until the actual stop location and values confirm it. A bounded wait timing out leaves the process running; inspect state or pause it, report that it has not reached the requested stop, and do not claim a handover is ready. Inspect errors for invalid conditions or unavailable symbols. If the breakpoint already exists, inspect the shared list and use the existing breakpoint or explicitly clear and recreate it; duplicate addresses are rejected by Delve.

## Hand over and take back

Use the user's requested editor, or the session's previous editor. `handover ID --editor vscode` selects VS Code; `--editor zed` selects Zed. Record the paused PID, goroutine, and PC and verify they survive attachment. Never resume merely to test handover. `editorConnected` indicates a socket; `editorReady` means DAP configuration completed.

### VS Code

The VS Code companion is optional and packaged separately. If it is not installed, use `code --install-extension ../../editors/vscode/debug-handover-0.1.0.vsix` when that artifact is bundled; otherwise build it from the plugin root with `npm ci`, `npm run build`, and `npm run package:vscode`. Run `handover ID --editor vscode`; the companion starts attachment through VS Code's public debugger API without a picker or launch.json. Verify `owner=vscode`, `editorConnected=true`, `editorReady=true`, `status=paused`, and the same PID/goroutine/PC. Inspect the native stack and source when desktop tools are available.

Automatic attachment requires an exact trusted local workspace and a fresh request. If it fails, inspect the error, companion installation, project window, and workspace trust. **Debug Handover: Attach Pending Session** retries, as does another `handover ID --editor vscode` while VS Code owns a disconnected pause. Do not create another target. A deliberate disconnect stays disconnected until retry or a fresh handover. Do not restart to restore a location if the user stepped during attachment.

The VS Code status bar and **Debug Handover: Give Control to Codex** command reclaim a settled pause and notify the linked task. `reclaim ID` also works quietly. Read fresh state on handback and follow the event checks below; a notification is not permission to resume.

### Zed

When the requested stop is reached, record the PID, stopped goroutine, and current thread's file, line, and PC from `state ID`. Run `debug-handover handover ID --editor zed` if Codex owns the session. This generates a named attach profile in the project's `.zed/debug.json`, opens the project and paused location, and gives Zed execution ownership. Existing JSONC profiles and comments are preserved. If Zed already owns the session, continue from its current connection state instead of reclaiming and handing over again.

**Complete attachment before handing back to the user.** The CLI's successful result means the profile is prepared, not that Zed is attached. When native desktop automation is available, use it to finish the handover:

1. Select Zed's window for the session project, bring it into focus, and inspect its current UI. If `zedConnected` is already true, verify the existing paused session instead of starting another frontend.
2. Otherwise open **debugger: start** (F4 with the default keymap), inspect the picker, and filter by the exact label returned by the CLI. Confirm that the matching profile is visible before activating it. Do not select a similarly named profile or launch task.
3. Inspect Zed for the paused source line and call stack, then read `state ID` again. Confirm `owner=zed`, `zedConnected=true`, `status=paused`, and the same PID, goroutine, and PC. A socket connection alone does not prove that the debugger UI finished attaching. Do not step or resume merely to test attachment.

Use the available computer-use tools for UI interaction, following their documentation. A request to hand over includes completing this attachment; do not routinely delegate the picker step to the user or ask them to approve it again. If the profile is initially missing, inspect the project window and allow its configuration to load, then retry once. If attachment cannot be completed or the state differs, report the actual state and the blocker without claiming success. Do not restart the debuggee to fix attachment. The user may have resumed or stepped during handover; never rewind or restart to restore the recorded location.

If native automation is unavailable, or the user explicitly wants a manual handover, provide the exact profile label and F4 fallback. Zed's CLI does not start the debugger itself. With a bound task, the browser button queues a message to Codex, which wakes to finish attachment through the desktop tools. With no bound task, the button only prepares the profile and opens Zed.

While either editor owns the session, use `state ID` for observation. The broker blocks Codex stepping and breakpoint changes. Do not bypass it by connecting directly to Delve. The user can step, inspect, and edit breakpoints in their editor while the panel follows the live backend.

When the user asks Codex to continue, inspect the state. If the editor is running, ask the user to pause there; reclaim requires a paused session. Then:

```sh
debug-handover reclaim ID
debug-handover state ID
```

Reclaim ends the editor's frontend debug session, leaving the original Delve process and debuggee paused. Re-read the stack, locals, and breakpoints; all may have changed during human control. Future handovers reattach using the same profile. Never reuse stale frame or variable data after execution resumes.

## Inspect and control

`state ID --goroutine N --frame N` loads that goroutine's stack and the selected frame's locals and source. `eval ID --expression 'items[0:64]' --goroutine N --frame N --depth 6 --count 64` reads values without resuming. `watch ID --expression EXPR` persists an expression evaluated in the selected frame at each pause; `unwatch` removes it. Watches may be out of scope in another frame. Evaluation supports field/index access, slicing, arithmetic, and len/cap/real/imag/complex; function calls, assignments, and channel receives are rejected. Maximum depth is 6, collection count is 128, and there are at most 16 watches. Inspection is also allowed while an editor owns a paused session. Without a goroutine, it follows Delve's currently stopped goroutine. Commands `next`, `step`, `stepout`, and `continue` support `--wait 20s`. `pause ID` halts a Codex-owned running process. `clear ID --breakpoint N` removes the specified breakpoint. The panel provides the same controls and a shared breakpoint list.

End with `stop ID` when the user is finished and has asked to end the debugging session. Closing the editor or the panel leaves the session alive. Handover must never call stop, restart, or rebuild. Zed handover and all handback buttons notify the bound Codex task with `codex queue`; VS Code attaches directly without an attachment notification. CLI Zed `handover` and `reclaim` are quiet unless `--notify` is explicitly set. Use quiet CLI commands when already handling an event so notifications do not loop. A notification is recorded as pending, sending, queued, failed, or unknown. Queued means accepted by Codex, not completed by the agent. Errors remain visible and `retry-notification ID` explicitly retries; after an uncertain timeout or broker crash, check the task before retrying to avoid duplicates.

On a debugger UI event, read current state and compare the event ID with the current notification before acting. Ignore obsolete events when ownership has changed or a newer event supersedes them. For handover, attach and verify as above. For handback, inspect the fresh pause and respond to the user's debugging context. Do not infer permission to resume execution just from receiving a notification.

Source fingerprints record the executable hash, available VCS build information, and project source hashes at launch. Treat `unverified` as unknown: a launch-time snapshot cannot establish that a precompiled binary was built from those files. Report changed source or binary flags when relevant; never rebuild implicitly.

`stop ID` removes only that session's generated Zed profile. `cleanup ID` removes a leftover profile after the session has ended and retains diagnostic logs; it refuses while known session processes are alive. A broker crash can be recovered while Delve survives, but the broker cannot recover memory after Delve or the target exits.
