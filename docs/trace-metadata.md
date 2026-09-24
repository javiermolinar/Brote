# Conversation and annotation trace schema

Brote keeps two trace identities per execution. Metadata adds short, zero-duration
spans at the time a message or annotation is recorded; it never mutates a capture
span or reopens execution. Captures are sampled debugger observations, not a full
execution profile.

| Content | Trace | Span name and primary string attribute |
| --- | --- | --- |
| Committed question or reply | Brote debugger/session | `brote.conversation` |
| Explicit human or agent note | Target program execution | `brote.annotation` |

The attribute contains the message/note text. `span.brote.conversation` and
`span.brote.annotation` are span-scoped query notation, not literal OTLP keys.
No annotations are synthesized by capturing, comparing, or discussing a run.

Every metadata span includes `brote.metadata.id`, `brote.metadata.revision`,
`brote.metadata.kind`, `brote.author`, and `brote.body.truncated`. Conversation
spans include `brote.thread.id`, `brote.message.id`, and `brote.question.id` when
present. Optional annotation attributes are `brote.label` and
`brote.comparison.key`. The span start/end timestamp is the recorded creation time.
Metadata bodies are bounded to 16,000 UTF-8 bytes; longer conversations retain
full local text and explicitly mark the exported body truncated. Explicit notes
exceeding the limit are rejected. At most eight evidence targets are accepted.

The durable record includes the exact service session identity, trace/root span
IDs, and evidence references. Shared sessions use `session:executionRun`; native
adapters use their existing service session key. Capture IDs map to actual span
IDs persisted by the service from ingested observations. Source filename/line
alone never identifies a capture. Saved legacy evidence without a capture span
may link to its known program root; no synthetic capture link is invented.

Each message/annotation revision has a deterministic span ID derived from its
trace, kind, stable record ID, and revision. Identical retries reuse it; conflicting
content for the same identity is rejected. A later note revision is a new span
with the same record ID. Local saved records remain authoritative. Export is
at-least-once after a crash, with the same span identity on retry; acceptance is
not proof that a backend query can see the span. Local and remote delivery status
are tracked independently. Backend retention may prevent late spans from joining
older stored traces; no query availability is promised merely from acceptance.

## Examples

- Question `q1`, thread `t1`: debugger trace A, text “Why is total 42?”, linked to
  program trace B, capture `c3` and its actual span. Reply `q1-answer` is a distinct
  conversation span in A, with the same evidence and question reference.
- Annotation `note1`, revision 1: program trace B, text “Cumulative total after the
  third addition”, optionally linked back to the reply span in A.
- Explicit two-run comparison `compare1`: one annotation in each selected program
  trace B and D with a shared stable ID/comparison key. The note targets both
  captures; any conversation reference points to the originating debugger trace.
  Creating this pair is an explicit operation, not a side effect of comparing.
- Continuing an existing discussion into another run preserves prior message
  ownership and evidence. Only newly committed messages are published there.

Discussion publication is derived from records committed in the same atomic
write as the message, so a failed save cannot create a conversation span. A durable
source index lets the shared tracing service discover committed metadata after
restart. Publication of metadata into closed runs uses a dedicated path that
validates trace identity and evidence; the execution span endpoint stays closed.

## Shared service operations

`POST /api/trace-metadata` accepts an immutable `traceinfo.Record`; its response
contains independent `local` and `remote` receipt states (`pending`, `accepted`,
`failed`, or `not_configured`). Normal execution batches still cannot append to
a finalized run. `POST /api/trace-metadata/sync` with `{"session":"BROKER_ID"}`
reconciles committed discussion records, including offline replies. The service
also discovers sources on startup and periodically while running.

`POST /api/annotations` takes `session` (the exact tracing-service session key),
`id`, `revision` (starts at 1), `author`, `body`, `targets`, and optional `label`,
`comparisonKey`, and `conversation`. Each target supplies `session`, `traceId`,
`spanId`, and optionally `captureId`. Targets are validated against the stored
trace/capture identities; comparison runs must belong to the same investigation.
A conversation reference must identify a known conversation metadata span.

The entire annotation revision and its per-trace records are saved before
publication. A successful response means the note is saved locally, even if an
entry in `export` is pending or failed. Resubmit the same ID, revision, and content
to retry; increment the revision by one to record an update. No records are
created by an ordinary capture, message, comparison, or list request.
`GET /api/annotations?session=SERVICE_SESSION` returns the latest annotation
revisions targeting that session, including notes authored from a compared run.
Older revisions remain in the local document and in the trace.

The broker exposes the same POST body at `/api/annotations`; GET uses
`traceSession=SERVICE_SESSION` to distinguish it from the broker authentication
`session` parameter. The broker requires the owner to be its own session. The
Go clients `tracing.Annotate` and `tracing.Annotations` also support saved runs
without a live broker. The user-facing CLI and browser controls are separate
implementation steps built on these operations.
