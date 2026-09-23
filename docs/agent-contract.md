# Common agent and discussion contract

This document specifies the consolidation contract. Capability
`coordination: 1` is advertised only when the broker implements it. Descriptor v2,
service API v1 and legacy commands remain readable; these types alone do not
advertise availability. Delve, execution fencing, capture and OTLP stay unchanged.

## Existing commands and extensions

| Existing family | Compatible extension / Go responsibility |
| --- | --- |
| `events SESSION --cursor --binding` | Raw observer stream remains; `--managed --consumer ID` provides delivery envelopes and host-liveness challenges over NDJSON. Stdin carries host facts and delivery outcomes. Go owns replay, cursor receipts and durable claims. |
| `event-status` | Common receipt arguments identify kind, subject, thread, attempt and recipient revision; the legacy handback form remains supported. |
| `task-start`, `task-heartbeat`, `task-execute`, `task-complete/cancel` | Existing explicit scope and bounded operations remain. Active host facts can maintain an acknowledged task; queueing/listening alone cannot. |
| `comment list/create/ask/reply/delivery/resolve/retry` | Explicit current/original context, previous session, recipient and attempt. Common reply identity is question + recipient revision + attempt + message ID. |
| `comment import` | Bounded, idempotent historical data import with existing IDs/mappings; not replay of live question creation. |
| `history`, `saved-run` | Shared Go readers; discussion-only writes use the same storage transaction with no Delve or target startup. |
| `tracepoint` / `breakpoint`, `captures` | Existing semantics and revisions; host tools simply expose them. Import carries the old `tracepointServiceIDs` mapping. |

Internal consumer open/next/fact operations reuse authenticated service action
transport. They are coordination operations, never arbitrary debugger forwarding.
A browser retains authenticated HTTP/SSE access to the same domain handlers.

## Delivery and recovery

A durable subject is a handback, execution task, or question. It carries one
current attempt, recipient, status and enough content to reconstruct delivery
without its event journal entry. Notification notes therefore survive the
256-event journal. Claim persists before exposing an envelope to the host.
Cursor persistence follows the durable subject write; a lagging cursor is safe
because replay cannot claim an already claimed subject. A failed persistence
must return no envelope. Subject state is authoritative, not the event stream.

Transitions distinguish pending → sending → queued, recipient acknowledgement
(acknowledged/thinking), persisted answer, failed, and unknown. Ack can beat the
sender receipt; late queued/failed/unknown cannot regress it. Receipt retries
for the same attempt are idempotent; conflicting/stale attempt or recipient
fails. An interrupted sending attempt becomes unknown on host replacement or
service recovery. No automatic reinjection of unknown sends. Explicit existing
retry commands create a fresh attempt. External exactly-once delivery is not
promised. Reconciliation scans durable handbacks, tasks and questions after
journal expiry and independently of observer cursors.

A service-issued consumer instance fences reconnects. A second instance cannot
finish a first instance's send or supply its lifecycle facts. Multiple observers
are allowed; one execution binding remains current. An observer disconnect is
not loss of a different valid host turn. Long-lived host invokers run outside
broker locks. Host-specific command invocation/message injection stays outside
the state machine; canonical envelope/message construction is Go-owned.

## Host facts and leases

Facts carry instance, monotonic sequence, turn, state and an optional response to
a fresh service challenge. States are active, idle and closed. Go requests
bounded liveness; hosts answer from their actual idle/settled/shutdown state.
Transport challenge responses are host observations, not host lease policy.
Unknown/expired/replaced facts cannot revive a scope. Active facts renew only a
current, explicitly acknowledged task associated with that host turn. A new
unrelated turn never adopts an old task. Idle at a settled pause completes;
idle while moving, closure, cancellation or liveness loss cancels and requests
Pause. Human interruption invalidates task and turn association before any
further renewal or movement. Service recovery does not restore active-host
proof. Codex queue has no assumed turn callbacks: task-execute/explicit bounded
heartbeats and expiry remain its safe path.

## Evidence, recipients and offline storage

Investigation, service session, execution run, pause epoch, evidence, question,
recipient and attempt have separate identities. Old message `run` fields that
meant a session remain readable; missing execution-run values stay unknown.
Evidence is immutable provenance. A historical evidence run does not invalidate
a current question after restart/exit, whereas an old attempt can never answer
a replacement question or authorize execution. Recipient selection (agent or
VS Code provider) never changes the execution binding. No automatic fallback.

Use one Go discussion transaction with an owner-only cross-process lock in a
stable per-discussion location. Live broker, offline CLI writer and history
mirrors share it; readers see atomic documents. Historical edits read/write the
same store after service exit without relaunching Delve. Live evidence capture
requires live service/run/epoch validation; historical answering does not.

Imports retain all previous turns, UTF-8 answers, source, partial/error metadata
and existing service IDs. Import accepts bounded historical answers up to
128 KiB (covering the old 32,000-character host bound), per-record evidence up to
1 MiB and aggregate documents up to 32 MiB. No silent truncation. On aggregate
or identity conflict, return an explicit recoverable result and preserve the
original host data. Test multibyte >16 KiB records and interrupted/duplicate
imports. Existing tracepoint mappings win over newly derived IDs; conflicting
mappings require visible resolution rather than creating duplicate definitions.

Models reason and stream in their hosts. Only final successful answers are
persisted; failed/cancelled streams update delivery status without masquerading
as an answer. Browser, CLI, VS Code and agents read the same question/evidence
IDs. Host preferences, drafts, decorations and selected frames remain UI state.

## Migration checks

Fixtures must cover journal-expired handback recovery, unknown-send retention,
claim/ack crash ordering, stale hosts and competing readers, lease loss and
human Pause, historical replies after restart/exit, concurrent offline writes,
lossless import and legacy tracepoint mapping recovery. Real packages must test
Pi injection/lifecycle/tools, Codex queue/ack/reply, VS Code provider and attached
agent questions, F5 and local Tempo. Explicit legacy Zed/RPC remains a documented
exception to migrated service consumers' no-direct-Delve rule.
