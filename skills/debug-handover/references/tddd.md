# Trace-Driven Debugging and Development

Read prior `brote comment list SESSION`, `brote query traces --session SESSION`,
and `brote annotation list SERVICE_SESSION` before reconstructing an investigation.
The service session is the exact `session` field returned by query traces, not a
source path or necessarily the broker session ID. Prior messages may refer to a
different run: preserve that provenance. A label is interpretation, not proof.

Record the question, hypothesis, expected observable change, and unresolved
uncertainty in the conversation. A debugger comment does not authorize execution
or code changes. Continue only within the user's existing debugging/development
request, using the task contract in SKILL.md. Obtain a baseline capture, make the
authorized change, build only when authorized, and capture a comparable run with
the same inputs, source point, and selected values. Compare actual values and
capture status before concluding; missing or truncated data cannot establish a fix.

## Worked baseline/change comparison

For a requested investigation of an unexpectedly large total, choose a verified
line after the addition and capture `attempt` and `total` under a stable name:

```sh
brote debug tracepoint add SESSION --client BINDING --file /absolute/main.go --line 19 --name total --values '{"attempt":"attempt","total":"total"}' --capture-limit 5 --scope run
brote debug task execute SESSION --task TASK --binding BINDING --operation continue --wait 20s
brote debug captures SESSION
brote query traces --session SESSION
```

Use the IDs returned by your run; these placeholders are not literal IDs. Record
the question and baseline values using the existing comment create/ask/reply
commands. For example, “At attempt 3, baseline total is 42; hypothesis: previous
calls are counted twice; expected after the authorized change: total 21.” This is
a hypothesis, not an observed result. After an authorized change and comparable
capture, report the measured value, whether it met the expectation, and remaining
uncertainty. If the measured value is still 42, say so; do not call the run fixed.
For a no-change control run, matching values test repeatability, not a repair.

Conversations are stored in the Brote `debugger` trace. Captured stacks/values and
explicit annotations live in the `program` trace. Query either returned trace ID:

```sh
brote query traces DEBUGGER_TRACE_ID
brote query traces PROGRAM_TRACE_ID
```

## Optional durable note

Use an annotation when the user requests a durable evidence note, or deliberately
choose it when it helps the current task. It is not a mandatory TDDD step. Do not
automatically generate baseline, regression, or fixed labels from captures,
comparisons, replies, or model conclusions. Keep notes concise and separate
captured facts from human/agent interpretation.

```sh
brote annotation create SERVICE_SESSION --id comparison-total-1 --capture CAPTURE_ID --body-file note.txt --author Brote
brote annotation list SERVICE_SESSION
```

The `captures` map from `query traces` pairs capture IDs with persisted span IDs.
An unknown capture is rejected. Legacy evidence without a capture identity can
be explicitly annotated at its verified program root by omitting `--capture`;
explain that it is a run-level note, not a link to a specific captured pause.
No debugger is started for saved-run annotation operations.

For a two-run note, put verified targets into `targets.json`, including the owning
service session. Both runs must belong to the same investigation:

```json
[
  {"session":"BASELINE_SERVICE_SESSION","traceId":"BASELINE_PROGRAM_TRACE_ID","spanId":"BASELINE_CAPTURE_SPAN_ID","captureId":"BASELINE_CAPTURE_ID"},
  {"session":"CHANGED_SERVICE_SESSION","traceId":"CHANGED_PROGRAM_TRACE_ID","spanId":"CHANGED_CAPTURE_SPAN_ID","captureId":"CHANGED_CAPTURE_ID"}
]
```

```sh
brote annotation create BASELINE_SERVICE_SESSION --id comparison-total-1 --targets-file targets.json --body-file note.txt --comparison-key total-at-attempt-3 --author Brote
```

Replace every placeholder with returned IDs. `--conversation-file PATH` optionally
links an existing conversation span: JSON `{ "session": "...", "traceId": "...",
"spanId": "..." }`, using the debugger trace and conversation span found by query.
Do not infer capture or conversation span IDs from a filename, line, or message ID.
The browser Annotations tab uses the same records and can show human/agent notes
together, with a View conversation link when the origin is available.

Retain ID, revision (initially 1), body, author, and targets for an exact retry.
A changed note needs the next consecutive revision; earlier revisions remain.
Success means saved locally. Check each `export` entry for local/remote pending,
accepted, failed, or not_configured status. Failed export does not undo the saved
note; retry delivery, not program execution. Query the destination to verify
visibility. Remote retention can limit late spans; local acceptance does not prove
remote visibility. End with the conclusion and unresolved questions in the
conversation whether or not you create an annotation.
