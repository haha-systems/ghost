# QAC agent protocol

Emit a `<QAC_REQUEST>` block only when cognitive reassessment is useful.

- Emit at most one block in a completed response.
- Use JSON with `direction`, `uncertainty`, `novelty`, `expected_gain`, and
  `failed_attempts`.
- Keep normalized signals in `[0,1]`.
- Do not include `importance` or name a destination resource.
- Keep evidence, attempted work, and recommended focus concise.
- QAC may reject the requested direction.
