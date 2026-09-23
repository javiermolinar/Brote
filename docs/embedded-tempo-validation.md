# Go-core Tempo validation

This document supersedes the earlier patched-Tempo and VS Code-owned integration
results. Those reviews, resource measurements and binaries do not validate the
current architecture. The current implementation uses unmodified Tempo from go.mod
and a shared Go tracing service for native VS Code and broker clients (Codex/Pi).

## Current checks (macOS arm64, 2026-09-23)

- Final `go test -race ./...`, `go vet ./...`, and `go mod verify` passed.
- Go capture tests exercise response/stop ordering, failure responses, thread isolation,
  linked roots, idempotent close and bounded snapshot payloads.
- Go service tests cover shared record reload/resume, host/origin/content-type checks,
  independent destinations, identical trace IDs, and sticky per-trace failures.
- Go remote-export tests cover verified TLS, untrusted certificate rejection,
  certificate configuration, trace-specific endpoint/header precedence and encoded auth.
- The real core integration started three concurrent clients and verified one service,
  captured a native observation through the API, launched a real Delve broker without
  an editor, queried both traces, stopped/restarted the service and queried saved data.
  Local trace export succeeded while the optional remote endpoint was unavailable.
- TypeScript build and the regular JavaScript suite passed: 89 passed, nine optional
  integration cases skipped, zero failures. The skipped shared-core case passed
  separately with real Delve in 60.5 seconds, including continuation of an active
  capture after a core restart; all eight skipped embedded-runtime
  cases also passed separately as described below.
- Producer timestamps survive asynchronous core startup, and the five native transport
  tests pass, including bounded backlog and configuration-object serialization.
  The extension no longer bundles an OpenTelemetry SDK.

The rebuilt macOS arm64 package passed the native VS Code/Delve host test with
12 spans, three snapshots and named value 42, using an isolated VS Code profile
and data directory. All 21 packaging/installer checks passed with real Codex, Pi
and VS Code CLIs in isolated profiles, including both Pi Git install/update cases.
The host-only release build produced eight artifacts locally; nothing was published.

The first stock large-trace run found 702 spans initially but 670 after maintenance.
A repeat reproduced it; reading retained Parquet showed the missing 32-span batch
was absent before compaction. Shared mutable batches in the destructive live-store
query combiner are the suspected cause; an isolated regression has not yet proved
the exact mechanism. Brote now uses stock backend-only query mode and tracks
expected span counts, avoiding this path without changing Tempo source.

With backend-only reads, the large-trace test passed in 133.8 seconds: all 702 spans
and all 700 snapshot payloads remained complete after actual compaction and restart.
The seven remaining runtime tests passed in 259.1 seconds, covering maintenance,
restart, flushed-data interruption recovery, immediate graceful shutdown, launch
failure, lock retry, slow startup and loopback listeners. These tests ran against
the current embedded-runtime read path; later changes to producer timestamps were
covered by the final Go, shared-core and packaged native-host checks.

Current logs live in ignored `dist/embedded-tempo/`, with the native host payload at
`dist/vscode-host-result.json`. They are local test evidence, not shipped runtime data.

## Limits

Actual Grafana Cloud ingestion has not been tested with authorized credentials.
Synthetic TLS/auth and an unreachable remote endpoint do not establish Cloud ingestion.
Only macOS arm64 has been executed during this correction. Final-source cross-builds
also passed for macOS amd64 and Linux amd64/arm64; these do not establish runtime
behavior on those platforms. Loopback listeners are accessible to other local processes.
Queries still bypass query-frontend and use backend-only mode. The profile cuts blocks
every 10 seconds and polls every 2 seconds, delaying recent-span visibility and creating
small blocks. Passing durability tests does not resolve this architecture tradeoff or
prove safe live reads. Frontend shutdown and live-read correctness remain separate
follow-ups, recorded in the personal-brain querier note. A fresh independent subagent
review found producer-loss status persistence, CLI error handling and cold-start
shutdown defects. These were fixed with regression coverage; interrupted-record
normalization and resumption were also hardened and independently rechecked.
The expanded Go CLI/Delve race integrations passed. Real isolated host installation
checks exposed a Pi Git-update fixture that built an unstripped test binary exceeding
the installer download limit; the fixture now uses the production release build flags.
Retention is finite (about 292 years). Tempo acknowledges before periodic WAL flush:
pre-WAL abrupt kills can lose recent spans. Power-loss and disk-full recovery are not
established. Producer queues can drop observations under overload; errors and recovered
open captures are marked incomplete. New measurements are required before quoting
RSS, disk growth or artifact-size numbers from the former implementation.
