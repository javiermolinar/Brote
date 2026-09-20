# Durable session history

The broker records investigation history independently of its runtime cache and
bounded live event stream. Closing, stopping, or cleaning up a runtime session
does not delete its history. This is captured evidence, not execution replay.

## Location and discovery

On macOS the default data directory is
`~/Library/Application Support/AgentDebugger`. Linux uses
`$XDG_DATA_HOME/agentdebugger` (or `~/.local/share/agentdebugger`); Windows path
resolution uses `%LOCALAPPDATA%/AgentDebugger`. The current broker's process and
locking support still targets Unix platforms. Set `AGENTDEBUGGER_DATA_DIR` to
override the data directory, including in isolated tests.

```text
projects/
  tempo/
    project.json
    2026-09-20/
      53684d66e4/
        session.json
        events.jsonl
        discussion.json
        snapshots/
          <sha256>.json
```

The date is the original session start date in UTC; it never changes at midnight
or on recovery. A project uses its repository's canonical Git common directory
as identity, so linked worktrees share a project. Non-Git projects use the
canonical workspace path. Names are sanitized, and an identity hash suffix is
added only when a name is already occupied. Separate clones are separate local
projects. Existing session IDs are looked up before allocating a new directory.

`agentdebugger history` lists saved metadata (title, project, binary, start,
last activity, status, and archive directory). `agentdebugger history ID` returns
ordered events. Both work without a live broker or runtime descriptor.
`agentdebugger sessions` retains its existing live-session semantics. The history
UI, title editing, search, and Markdown export are later plan steps.

## Format

Every newline-terminated JSON record has `v`, `seq`, `at`, `type`, `actor`, and
`data`. Version 1 uses UTC timestamps and a strictly increasing per-session
sequence. This sequence is independent of the bounded SSE cursor.

```json
{"v":1,"seq":2,"at":"2026-09-20T10:00:01Z","type":"execution.stopped","actor":"debugger","data":{"stop_id":"abc123","context_id":"1","reason":"stopped","location":{"file":"main.go","line":18},"snapshot":"snapshots/<sha256>.json"}}
```

The first event captures launch metadata, arguments, and binary/source
fingerprint. Subsequent events record broker/editor lifecycle, execution and
breakpoint actions and errors, stops, inspected values, discussion changes and
delivery acknowledgement, and session end. Editor protocol observations are
recorded separately as `editor.<DAP command>` with request, result, and error;
any DAP handles there are historical, not reusable live references. Execution
task cancellation will be recorded when execution tasks are implemented.

Stop snapshots capture a bounded stack without loading every variable. Full
inspections capture only the bounded values the existing inspector requested;
identical snapshots during a pause do not append repeated inspection events.
Snapshots use `contexts`, whose entries carry an ID, extensible `kind`, frames,
and observations. The Go adapter supplies `kind: "goroutine"`; the envelope has
no Go-specific field. Stop IDs distinguish observations across pauses. Explicit
evaluation results are stored with their action event.

Files in `snapshots/` are immutable and content-addressed. A snapshot is written
before the event referencing it. An interrupted write can leave an unreferenced
snapshot; it never exposes a partially written snapshot via a committed event.
`session.json` is a catalog projection. Lifecycle fields are replayed on broker
recovery. A detached broker is distinguished from an ended target; a hard kill
cannot emit a disconnection event, so catalog status is last recorded status,
not a current process health check.

Existing discussions are copied on first recovery into `discussion.json`, with
all thread/message IDs and captured context preserved. Each subsequent mutation
records an immutable discussion snapshot; old Go-specific captured contexts are
retained verbatim for compatibility. Offline comment reads prefer the migrated
copy and fall back to the old discussion store. The old copy is not deleted.

## Durability

A per-session exclusive lock enforces a single journal writer; a separate lock
serializes project name allocation. Events are synced before catalog updates.
On recovery, only an incomplete final JSONL line is truncated. Malformed complete
records, unsupported versions, or sequence gaps fail explicitly rather than
silently losing evidence. Journaling failures appear as `historyError` in live
state; they do not silently change debugger execution results.

History contains program arguments and inspected values. No automatic deletion
or retention limit is applied in this version. The core neither records every
UI refresh nor promises to reconstruct unobserved memory, external inputs, or
execution schedules.
