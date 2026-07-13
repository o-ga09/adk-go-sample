# Tool JSON schema inference invariants

The ADK's `functiontool` infers a JSON schema from a tool's input/output
structs (`jsonschema.For[T]`) and validates **every call and every result**
against it. A validation failure fails the whole tool call at the ADK layer:
the handler's result is discarded and the LLM receives `{"error": <error>}`,
which serializes to an empty `{}` (this is how the 2026-07-11 batch reported
"メールの取得に失敗" to Slack on a zero-unread-mail day).

When defining tool input/output structs in `internal/tools/`:

- **Every optional field must carry `omitempty`.** Fields without it become
  `required` in the inferred schema, so the LLM omitting one (e.g.
  `maxResults`, or `events` on a day with no calendar registrations) fails the
  call before the handler even runs.
- **Result slices must never serialize to `null`.** Give result slices
  `omitempty` (or always assign an empty slice). Historically a nil slice
  without `omitempty` failed validation outright (`got null, want array`) even
  though the handler succeeded; jsonschema-go v0.4.x relaxed this and now
  accepts `null` for arrays, but keep `omitempty` regardless — a `null` in the
  result JSON is still noise the LLM shouldn't have to reason about, and the
  invariant must not silently depend on the looser validator.
- Fields the handler genuinely cannot work without (`query`, `messageId`,
  RFC3339 times) should stay required — schema rejection with a clear message
  is better than the handler failing oddly.

Regression tests in `internal/tools/{gmail,calendar,notify}` replay this
validation (`jsonschema.For` → `Resolve` → `Validate`) against representative
payloads; extend them when adding tools or fields. The project uses
`github.com/google/jsonschema-go` v0.4.2 (pulled in by `google.golang.org/adk/v2`;
before the ADK v2 migration it was pinned to the stricter v0.3.0). v0.4.x's
validator is looser — notably it accepts `null` for arrays — so the regression
tests assert the *positive* case (representative payloads validate) rather than
relying on the validator to reject `null`. Keep the `omitempty` invariants above
independent of validator strictness; do not loosen a struct tag just because the
current validator would tolerate it.
