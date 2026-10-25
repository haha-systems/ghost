# Memory and CES

Status: Milestone D, Step 1 defines the core contract, service, context builder, bounded writer, and fake backend. Runtime hooks and the Ghostdive adapter are not connected.

CES is the record of evidence, current beliefs, work, and phase changes. Memory
is a separate source for reviewed knowledge that may help a later task. A
memory result carries its source and retrieval time. CES can cite it as
evidence; it cannot silently replace CES state or prove that a new task is
complete.

The current `epistemic.Store` is in-memory CES state. Ghost has memory settings
in `config.MemoryConfig`, but no runtime memory store is connected. See
[`epistemic.Store`](../../internal/epistemic/store.go),
[`MemoryConfig`](../../internal/config/config.go), and [CES](../CES.md).

Step 1 now defines backend contracts, provenance, scope enforcement, and safe
context preparation. Storage and retention policy beyond these rules remains
for later work.

## Milestone D Step 1 design

## Ownership and boundaries

`internal/core` defines backend-neutral memory scopes, claim kinds, validation, provenance, records, recall and write requests, and the backend interface. `internal/memory` applies scope rules, normalises provenance, builds the context block, and owns one bounded write worker. The backend receives a resolved Ghostdive space with each request. The fake backend is under `internal/memory/memorytest`; its token-overlap ranking is test-only.

Recall is synchronous and has a deadline. A caller must run it at a safe task boundary outside the controller owner loop. Writes enter a bounded queue and do not wait for storage. A disabled service emits no events and does no work. An enabled service with a missing backend or workspace space emits `memory.unavailable` and stays inactive.

This follows the runtime and event boundaries in [runtime design](runtime.md) and [communication design](communication.md). Step 1 does not add a dependency on `internal/run`.

## Scopes and spaces

Ghostdive spaces are the only storage isolation boundary. The service chooses spaces and checks the decoded Ghost envelope after retrieval:

| Scope | Space | Read rule |
| --- | --- | --- |
| `agent` | One configured space for that agent | Only the owning agent can read it. If no space exists, skip the write and read. Never use the workspace space as a fallback. |
| `task` | Workspace space | Only the same task or a descendant task can read it. The task ID is in the envelope. |
| `workspace` | Workspace space | Any configured agent in that workspace can read it. |
| `global` | Separate configured space | Read and write only when `share_global` is true. It is off by default. |

Records returned from a space are post-filtered by their envelope. The service counts filtered records and emits `memory.scope_filtered`. This is required because Ghostdive retrieval has no agent or provenance filter.

## Ghostdive mapping

The later HTTP adapter will use only the API described in the milestone D spec: observe at `POST /v1/spaces/{spaceID}/memories`, retrieve at `POST /v1/spaces/{spaceID}/memories/retrieve`, and read by ID at `GET /v1/spaces/{spaceID}/memories/{recordID}`. Observe sends a layer, text fields, explicit confidence, agent ID, provenance, evidence references, and `skip_auto_supersede=true`. Retrieve sends a query, top-k limit, mode, layers, and states. Responses map the documented record fields and hit score into `core.MemoryRecord`. Requests use bearer auth. Network, timeout, and 5xx errors are unavailable; 4xx errors are rejected; 429 is quota exceeded. The adapter will not call the transition endpoint. No Ghostdive HTTP client is part of Step 1.

The later adapter will write provenance as `source_kind = ghost/<claim kind>`, `source_ref = ghost://<workspace>/<scope>/<agent or task>`, and a versioned JSON envelope in `provenance.note`. A challenge uses `source_record_id` to link to the record it disputes. Event and artifact references go in Ghostdive's `evidence_refs`; the full evidence and counterevidence lists remain in the envelope. Envelope encoding and decoding are deferred to that adapter. Unknown fields must be tolerated. Missing, malformed, or unrelated provenance is foreign data: show its reported confidence, mark its evidence missing, and keep validation unverified. The fake backend stores the decoded core fields so service scope filtering follows the same rules.

## Provenance and conflict rules

The service sets confidence explicitly and applies these upper limits: observation 0.6, inference 0.5, hypothesis 0.3, instruction 0.7, and verified result 0.9. Confidence has a 0.01 floor. A verified result without evidence stays unverified and is capped at 0.5. A claim is never promoted to verified unless it is a verified result with at least one evidence reference. Normalisation does not otherwise upgrade validation.

Every observe request sets `skip_auto_supersede=true`. A correction or challenge is a new record linked to the challenged record. Retrieval keeps both records and marks both as disputed when both are present. A Ghostdive `deprecated` or `revised` state is shown as superseded; an `accepted` state alone does not verify a claim.

Retrieved text is evidence, not instruction. The context builder sorts by score and then ID, strips control characters, replaces backticks, collapses whitespace, caps each record, and enforces a token and record limit. It marks disputed and superseded records. The block header states that the content does not override instructions or permissions. Defaults are about 800 tokens and 8 records.

The single write worker has a queue limit of 64 and a 5-second per-write timeout. Retrieval defaults to a 2-second timeout. A full queue drops a write and emits an event. A failed write emits its error class. A close deadline cancels pending work and reports each unfinished write as lost. Retrieval failures return an empty package and emit `memory.retrieval_failed`.

## Events

Memory events use `CategoryMemory`: `memory.unavailable`, `memory.retrieval_started`, `memory.retrieval_completed`, `memory.retrieval_failed`, `memory.context_injected`, `memory.write_queued`, `memory.write_stored`, `memory.write_failed`, `memory.write_dropped`, `memory.write_skipped`, `memory.write_lost`, `memory.challenge_recorded`, and `memory.scope_filtered`. Completion events report counts, latency, scope violations, conflicts, injected IDs, and token use. Write events include bounded summaries and evidence references. No event contains an API key.

## Planned runtime hooks

Step 2 will recall agent, task, and workspace memories when a human starts a task and when a delegated task is delivered. It will add the context package to the existing safe-boundary input. Explicit recall and remember blocks, challenges, task completion, and task failure will use the same service. Captures will be queued; reads will not trigger consolidation. Runtime recovery and the TUI inspector will use memory events in a later step. Memory will never be edited into a live provider turn.

## Ghostdive #42 assumptions

The adapter will keep these assumptions local so later provenance or encryption work needs changes only in `internal/memory/ghostdive`:

1. The HTTP JSON shape stays as documented, including the observe, retrieve, and record fields.
2. Ghost does not depend on server-side filtering or search over provenance, evidence references, or agent IDs. It selects spaces and filters decoded envelopes in the client.
3. The adapter accepts unknown JSON fields and missing optional fields.
4. A 5xx response that means data could not be opened is unavailable, not an empty result.
5. The Ghost envelope stays in sealed `provenance.note` and `source_ref`, where it remains private and tamper-evident; Ghost does not need SQL filters over it.
6. A ranking change does not affect scope or provenance correctness.

## Not done in Step 1

There is no Ghostdive HTTP adapter, config extension, runtime wiring, task hook, explicit memory block parser, restart recovery, TUI inspector, or live Ghostdive check. No key is loaded, no network call is made, and `ghost.toml` is unchanged. The fake backend does not model Ghostdive ranking, storage, encryption, or promotion.
