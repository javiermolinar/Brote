# Investigation workspace

`agentdebugger ui` starts or finds the persistent local workspace and prints its URL.
The workspace stays available after a debugger broker or target ends. `start` now
returns a workspace link selecting the new run; `--no-ui` retains the standalone
broker URL for headless use. Existing direct broker links continue to work.

An investigation groups runs explicitly. Existing sessions appear as single-run
investigations; sessions are never grouped merely because their project matches.
New investigations accept a title, project directory, and precompiled executable.
Run again reuses the archived executable path and program arguments and adds a new
run to the same investigation. It does not copy agent bindings or breakpoints and
never recompiles. `start --investigation ID` also joins an existing investigation.
The current implementation launches Go targets with Delve; generic connection labels
do not imply that other adapters or external-process attachment are implemented.

Metadata lives in the AgentDebugger data directory under `investigations/`, with a
separate run association under `investigations/runs/`. Recorded evidence remains in
`projects/<project>/<date>/<run>/`. Runtime-cache cleanup does not remove that history.

## Discussions

The source heading displays the project-relative path when available and otherwise
the absolute path. Copy path copies the absolute path. Source bubbles open a thread
in the right panel. New question creates a separate thread; All discussions returns
to the list. Replies explicitly select current pause or original saved snapshot.
The broker checks the generation before capturing a fresh reply. Each human message
retains its context and originating run; the thread context remains the latest
question context for compatibility with existing agents.

An earlier thread may be continued in a later run of the same investigation. The
new run copies its previous messages and adds the follow-up without rewriting the
older run's archive. The workspace shows the latest copy of each thread. Agent
bindings are local to a run; reconnect the agent to receive the follow-up there.
Old brokers that do not advertise `replyContexts` offer original-snapshot replies only.
Ended runs expose captured source and values read-only; they do not replay execution.
The first history view shows the last recorded inspection, not a complete stop timeline.

## Connections and cleanup

Program, debugger, and agent states are independent. Agent offline does not prevent
ending a run. End run explicitly terminates the launched target and debugger but
retains the investigation. A lost connection is not treated as proof that processes
have exited. Recovery instructions use `recover ID`, then `end-session ID --confirmed`;
no agent is required. Historical records without verified termination are labelled
as unverified, rather than stopped. Reconnect agent provides Pi's `/debug-connect ID`
or generic existing-run instructions for other harnesses.

## Workspace endpoints

- `GET /api/workspace`: grouped live and recorded runs.
- `GET /api/saved-run?id=ID`: last recorded inspection and durable discussions.
- `POST /api/runs/start`: `{run: ID}` to rerun, or `{title, project, binary}` to launch.
- `POST /api/sessions/stop`: `{id, confirmed: true}` to terminate a launched run.
- Existing broker APIs use `?session=ID` through the workspace proxy.

The UI service binds only to loopback, verifies Host and Origin, and accepts JSON
for mutations. It forwards requests only to validated recorded loopback brokers.

### Run navigation and cleanup

The investigation heading shows its immutable creation date. Legacy investigations
are backfilled from the earliest known run before deletion, so removing that run
does not change the heading date. Each run has its own compact local timestamp.

A run header shows its state and location; debugger details are behind Details.
Agent connection controls live beside discussions. Archived runs hide execution,
expression evaluation, and breakpoint creation. Missing snapshots do not hide saved
discussions. Browse saved files opens only source captured in the last inspection;
this is not the full historical source timeline. Older runs without saved launch
arguments cannot be rerun from the UI.

Run and investigation rows expose actions through the ellipsis menu. Deletion is
explicitly confirmed and only permitted for ended runs. Whole-investigation deletion
preflights every member, including pending launches, while coordinating with new
run assignment. Runtime/history locks prevent deleting records still in use. Files
are staged by rename before erasure; rename failures restore earlier paths, and
cleanup failures report the remaining staged location. This is not a cross-filesystem
crash-atomic transaction. Continued discussion copies in other runs remain intact.
