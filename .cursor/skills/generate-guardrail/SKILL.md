---
name: generate-guardrail
description: Generate a Hoop guardrail regex from a plain-language description and prove it with the warden CLI. Use when the user asks to create, generate, or write a guardrail, block rule, or regex for queries, and validate it against examples.
---

# Generate a guardrail, prove it with warden

Turn "block queries like X" into a Warden rule file whose pattern is verified
against real examples by `warden test`, not eyeballed.

## Workflow

1. **Collect intent and examples.** You need at least 2 queries the rule must
   block and 2 it must allow. If the user gave only a description, write the
   examples yourself from it and show them; near-misses matter most (the
   allowed query that looks like the blocked one).

2. **Write the rule file.** Schema (`pkg/rules`):

```yaml
name: "update-no-where"
description: "Blocks UPDATE statements missing a WHERE clause"
message: "UPDATE without WHERE touches every row. Add a WHERE clause."
rules:
  - type: "pattern_match"
    pattern_regex: "(?is)^\\s*update\\s+..."
examples:
  must_block:
    - "UPDATE users SET active = false;"
  must_allow:
    - "UPDATE users SET active = false WHERE id = 7;"
```

   For exact substrings, prefer `type: "deny_words_list"` with a `words:` list
   (case-sensitive contains) over a regex.

3. **Validate.** Build the CLI if needed (`make build` in the repo root), then:

```bash
warden test -json rule.yaml
```

   Exit 0 means every example behaved. On failure, the JSON names each miss:
   `expected: "block", got: "allow"` is a false negative (pattern too narrow),
   the reverse is over-blocking (too broad). Revise the pattern and rerun.
   Do not deliver a rule that has not passed.

4. **Deliver.** Hand back the passing YAML and note that the same rule imports
   into the builder at hoop.dev/labs/warden and exports as a Hoop `/api/guardrails` payload.

## Pattern constraints

- **Stay in the RE2-portable subset**: no lookahead `(?=`/`(?!`, lookbehind, or
  backreferences `\1`. Hoop's built-in engine (Go RE2) and `warden test` reject
  them. If a deployment uses the Presidio analyzer (Python re), a lookahead
  variant may be added as `pattern_regex_python`, but `pattern_regex` must
  stay portable because that is what gets tested.
- Matching is **unanchored and case-sensitive** by default. Use `(?i)` for
  case-insensitivity, `(?s)` to let `.` cross newlines, `^` with `(?m)` only
  when you mean per-line. `(?is)` at the start covers most SQL rules.
- Use `\b` word boundaries around keywords (`\bdrop\b`, not `drop`) or the
  rule blocks `backdrop`.
- "Does not contain X" is hard in RE2. For no-WHERE-style rules, match the
  statement head (`(?is)^\s*update\s+`) and put the WHERE-carrying variant in
  `must_allow` to check it; if that over-blocks, say so instead of reaching
  for lookahead.
- YAML double-quoted strings eat backslashes: write `\\b` inside `"..."`, or
  use single quotes where `\b` survives as-is.

## Failure loop budget

Iterate up to 5 times. If the examples still fail, the intent likely cannot be
expressed as one portable regex; report which example pair conflicts and ask
the user to split the rule or relax an example.
