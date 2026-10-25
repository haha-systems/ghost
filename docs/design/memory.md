# Memory and CES

Status: Designed. Implementation is not started.

CES is the record of evidence, current beliefs, work, and phase changes. Memory
is a separate source for reviewed knowledge that may help a later task. A
memory result must carry its source and retrieval time. CES can cite it as
evidence; it cannot silently replace CES state or prove that a new task is
complete.

The current `epistemic.Store` is in-memory CES state. Ghost has memory settings
in `config.MemoryConfig`, but no runtime memory store is connected. See
[`epistemic.Store`](../../internal/epistemic/store.go),
[`MemoryConfig`](../../internal/config/config.go), and [CES](../CES.md).

Future memory reads and writes should emit normalised memory events with
provenance and permission checks. The storage and retention rules remain to be
designed before implementation.
