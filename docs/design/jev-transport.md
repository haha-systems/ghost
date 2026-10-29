# Jev transport proposal

Status: Proposal for Mark to choose. Nothing here is implemented. Jev does
not exist as a service yet, and [decisions.md](decisions.md) does not specify a
transport, so this repo has no transport stub.

## What the contract already needs

These parts already exist in code. A transport only has to carry them:

- **Request.** `cognition.JevInput` serialised as JSON. It is a compact
  allocation-boundary state: phase, current resource, effort, attempt and
  reopen counts, artifact validity, verified progress (state, provenance,
  strength), unknown and contradiction counts, and a scope estimate. By
  design it contains no transcript, goal text, repository text or model
  output (`internal/cognition/jev.go`).
- **Response.** One JSON object that `cognition.ParseJevJudgment` accepts.
  It has exactly `current_resource_adequacy`,
  `expected_gain_from_stronger_cognition`, `decomposition_need` and
  `confidence`, each a number in [0,1], plus `judgment`, which is one of
  `continue_current_resource`, `escalate_cognition`, `reframe_or_decompose`
  or `insufficient_evidence`. Unknown fields and trailing JSON are rejected.
- **Go boundary.** `cognition.JevClient`, whose signature is
  `Assess(ctx, JevInput) ([]byte, error)`. The raw bytes go to the strict
  parser unchanged, so the transport does no interpretation.
- **Callers.**
  - `Coordinator.SetJev`, the advisory QAC signal. It is only asked about
    repeated EXECUTE attempts with structured low progress.
  - `decision.NewJevAgent`, for decision kind `cognitive_adequacy`. The
    decision service already applies the timeout, run and task budgets and
    the failure breaker.
- **Failure semantics.** Jev is advisory (A3). A timeout, transport error or
  invalid output becomes an ignored assessment or an `invalid` or `error`
  decision outcome, and never fails the run. Jev cannot choose a resource,
  model, provider, agent or effort.

The transport therefore needs one request, one response, a context deadline,
and an error. It does not need streaming, sessions, or Jev calling back into
Ghost.

## Option A: JSON over a stdio subprocess, one process per call

Ghost runs a configured command, writes one `JevInput` JSON object to stdin
and closes stdin. It reads stdout, bounded to 64 KiB, as the judgment and
passes it to `ParseJevJudgment`. The call fails if the exit status is
non-zero or the context deadline passes; on a deadline Ghost kills the
process. Stderr is discarded or logged at debug level, never parsed.

```toml
[decisions.jev]
command = ["jev", "assess"]   # argv, no shell
timeout = "5s"
```

- Pros:
  - This is the smallest option, at about 60 lines plus tests.
  - It has no network, ports or authentication.
  - The wire format is exactly the two existing JSON shapes, so there is no
    envelope or versioning to invent.
  - It is trivially testable with a fake script.
  - Jev can be any language, a wrapper around a local model, or a rules
    script.
- Cons:
  - Each call pays process start-up cost. That's acceptable here because Jev
    is only consulted on repeated low-progress EXECUTE attempts, which are
    rare.
  - Jev must be installed on the same machine.

## Option B: HTTP POST to a Jev service

`POST {base_url}/v1/assess` with `Content-Type: application/json`. The body is
the `JevInput` and a 200 response body is the judgment JSON. Any other status
is an error, with no retry inside the call. An optional bearer key is read
from an environment variable, following the Ghostdive client pattern
(`$ENV` reference, never logged, redirects not followed, bounded body).

```toml
[decisions.jev]
url_base = "http://127.0.0.1:8095"
auth = "bearer_env"
key = "$JEV_API_KEY"
timeout = "5s"
```

- Pros:
  - Jev can be a long-lived or shared service, which fits if it becomes one,
    and it can be hosted next to Ghostdive.
  - There is no per-call process start-up.
- Cons:
  - It needs a running service plus authentication, health checks and
    availability handling, and a remote Jev could carry per-call cost.
  - It means a small amount of new protocol (a path and status codes).

## Recommendation

Start with **Option A**. It is the smallest transport that satisfies the
existing contract with no new protocol surface, and it keeps Jev local and
free by default. Because both options carry the same bytes behind
`JevClient`, moving to Option B later is a new `JevClient` implementation and
a config table, with no change to the coordinator, decision service or
parser.

Either option would be wired the same way. Construct the client from
`[decisions.jev]`, then call `Coordinator.SetJev` and `SetJevTimeout`, and
register `decision.NewJevAgent` when the table is present. With no table
configured, nothing changes.
