# Native macOS process trial

Based on v1.4.4. The upstream binary already starts an ephemeral actions/runner
on Darwin ARM64 from an Aqua LaunchAgent. A real shell job passed on 2026-11-15:
https://github.com/jai/ci/actions/runs/43708103949

Two opt-in additions are being evaluated for upstream:

- `agent.process_term_only`: send TERM and wait. On timeout, report an error and
  keep the process directory. Never escalate or report that a live runner was
  removed. This is for native build systems that are damaged by forced shutdown.
- `agent.process_admission_file`: read a local JSON slot policy before registering
  a runner. Reserve with atomic mkdir, bind the owner to the runner PID and release
  after directory removal. An occupied slot is a retryable backend-busy result,
  so GitHub has no runner to assign a job to until the host can run it.

Example policy (write atomically and refresh before expires_at):

```json
{
  "directory": "/Users/jai/.local/state/ci-slots/mac",
  "capacity": 2,
  "expires_at": 1794746400,
  "env": {
    "HOME": "/Users/jai",
    "USER": "jai",
    "LOGNAME": "jai",
    "GATE_SLOT": "zoomies-c03-{slot}",
    "TMPDIR": "{job}/tmp",
    "CI_NINJA_JOBS": "4",
    "GRADLE_OPTS": "-Dorg.gradle.workers.max=4"
  }
}
```

`CI_SLOT` is always the reserved number. The environment map is an explicit
operator allowlist; the agent's credentials are never inherited. The policy
supervisor belongs to jai/ci, which owns the fixed slot limits. No input-idle
sampling is used. Disk admission thresholds are a separate change. A missing, expired or invalid policy fails closed. Capacity zero pauses
admission while existing runners finish. Other owners' reservations are never
reclaimed by this implementation. After a crash, inspect stale reservations
before removing them; prefer a stranded slot to over-admitting work.

Set controller host `auto_adjust` to `off`, and pin capacity independently of
agent enrolment: the stock controller raised a newly joined one-slot Mac to
four slots. Do not turn these options on for Windows.

Tests exercise concurrent admission, another owner's reservation, expired
policies, slot environment, TERM-responsive shutdown and a TERM-resistant
process whose job folder must survive a removal request. Run with:

```sh
go test -race ./internal/backend -run 'TestNative|TestTermOnly' -count=1
go test ./internal/config
go vet ./internal/backend ./internal/config
```

Not yet an upstream submission. Known follow-ups before proposing a public API:
separate the compatibility owner-file contract from the generic slot interface,
validate policy input as configuration, and prove crash recovery and process-tree
shutdown (including descendants that leave the listener's process group).

The live trial uses `process_term_only: false`: Darwin sends TERM first, then
escalates only if the runner does not stop. The opt-in TERM-only mode remains
available. A real cleanup retry exposed a deleted `.zoomies-slot` record;
removal now recovers the lease from the matching slot record and refuses to
release it while its owner or process group remains alive.
