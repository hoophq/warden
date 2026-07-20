# Warden

Author, test and CI-check [Hoop](https://hoop.dev) guardrail rules in the browser and
on the command line. The builder lives at
[hoop.dev/labs/warden](https://hoop.dev/labs/warden); the CLI is one Go binary. Your
queries stay on your machine.

Writing guardrails means writing regex against adversarial SQL. It is hard to tell
whether a rule blocks what you intend without over-blocking good queries. Warden gives
you several ways to build a rule, a test bench to prove it against your own examples,
and exports for each engine in the Hoop ecosystem.

## The builder

A fully client-side page at [hoop.dev/labs/warden](https://hoop.dev/labs/warden)
(source lives in the hoop.dev landing-page repo, under `components/labs/Warden.jsx`
and `lib/warden/`). Everything runs in the browser; permalinks jump straight into the
builder with the rule pre-populated.

- **Start from a query, or a pair.** Paste a query you want to block, plus an optional
  similar one that must stay allowed. Warden recognizes the shapes (DELETE without WHERE,
  `WHERE 1=1` tautology, CTE-wrapped writes, stacked statements), suggests vetted rules
  pre-filled from the queries themselves, and shows per-suggestion verdicts before you
  commit.
- **Curated pattern library** with parameterized templates. Each ships with its own
  allow/block examples, including the queries it is known to miss.
- **Strict mode for no-WHERE rules.** RE2 lacks negative lookahead, but "contains no
  WHERE" is still a regular language. Warden generates the exact complement pattern from
  a KMP automaton, property-tested against `String.includes`.
- **Raw regex** and **deny-words** editors, a **test bench** with live verdicts and match
  highlighting, and a **plain-English explainer**.
- **Shareable permalinks.** The rule and its test examples serialize into the URL hash,
  so a link carries the whole working state without a server or an account.

## Exports

| Format | Dialect | Destination |
| --- | --- | --- |
| Hoop curl / JSON | portable RE2 subset, or Python `re` for Presidio-backed deployments | `POST /api/guardrails`, or paste into the Hoop UI |
| Warden rule file | both | git + `warden test` in CI |

The Hoop export is a ready-to-run curl that reads credentials from a logged-in hoop
CLI, so shipping a rule is copy, paste, enter:

```bash
curl -sf -X POST "$(hoop config view api_url)/api/guardrails" \
  -H "Authorization: Bearer $(hoop config view token)" \
  -H "Content-Type: application/json" \
  -d '{ ... the exported payload ... }'
```

Python has lookahead, so the Presidio-engine variant of "UPDATE without WHERE" is the
readable `(?is)^\s*update\s+(?!.*\bwhere\b)` while the portable export uses the RE2-safe
pattern. One rule, correct syntax for each engine.

## The CLI

Install with Homebrew, or build from source (`make build`, Go only, no npm):

```bash
brew install hoophq/tap/warden
```

Three commands:

```bash
warden test examples/update-no-where.yaml   # CI: prove rules on their examples
warden scan -custom pack.yaml results.csv   # PII detection over sample output
warden generate -block "DELETE without WHERE" \
  -query "DELETE FROM users;" -o rule.yaml   # LLM drafts, warden proves
```

- **`warden test`** loads Warden rule files (rules plus `must_block` and `must_allow`
  examples) and exits non-zero on any false positive or negative. Commit rules to git,
  run this in CI, and guardrails become code. `-json` emits each example's verdict as
  structured data.
- **`warden scan`** runs [Alcatraz](https://github.com/hoophq/alcatraz) in-process over
  sample query output: 45 entity types, checksum-verified (Luhn, CPF mod-11), zero
  network. `-custom` adds your own recognizers from YAML (see
  `examples/custom-recognizers.yaml`). Exit code 3 means PII was found.

## Generating rules with an LLM

`warden test` gives a coding agent a mechanical oracle: the agent writes the regex,
Warden proves it against the examples with the same engine Hoop compiles. Three ways
to run that loop:

- **`warden generate`** drives it from the command line. It prompts an LLM with your
  intent and sample queries, parses the returned rule file, runs every example through
  the matcher, and feeds failures back until the rule passes (or `-max-attempts` runs
  out). The default LLM is Claude Code headless (`claude -p`); `-llm-cmd` swaps in any
  command that reads a prompt on stdin and prints the rule. Your `-query` and
  `-allow-query` values are required verbatim in the examples, so the queries you care
  about are always the ones tested.

```bash
warden generate -block "UPDATE or DELETE that hits every row" \
  -query "DELETE FROM users;" \
  -allow-query "DELETE FROM users WHERE id = 1;" \
  -o rules/no-mass-writes.yaml
```

- **The agent skill** (`.cursor/skills/generate-guardrail/` for Cursor,
  `.claude/skills/generate-guardrail/` for Claude Code) teaches an interactive agent
  the same workflow: draft the YAML, run `warden test -json`, revise on failures.

- **The Claude Code hook** (`.claude/settings.json` + `.claude/hooks/warden-test.sh`)
  enforces the invariant from the other side: whenever an agent edits a YAML file that
  looks like a rule file, the hook runs `warden test` on it and feeds failures back
  into the session. An agent cannot leave a broken rule behind, whatever it was doing.

```bash
warden test -json rule.yaml
# { "pass": false, "results": [ { "example": "UPDATE users SET x=1",
#     "expected": "block", "got": "allow", "pass": false }, ... ] }
```

## Engine fidelity

Hoop evaluates guardrails with one of two engines, chosen by deployment config

- **Presidio analyzer** (Python `re`), when a MSPresidio DLP provider is configured.
  Guardrail patterns ship to it as ad-hoc recognizers, so Python syntax including
  lookahead is accepted there.
- **Built-in fallback** (Go RE2, `localguardrails`), when no Presidio is available.
  It rejects lookahead, lookbehind, and backreferences.

Warden's default export is the **portable subset**: patterns that parse and behave the
same under both engines. The Hoop export tab also offers a Presidio-engine variant that
uses Python lookahead where it reads better.

- The CLI (`pkg/match`) runs Go's `regexp`, which models the fallback engine exactly and
  the Presidio engine for portable patterns.
- The browser matches with [re2js](https://github.com/le0pard/re2js), an RE2 port, so it
  flags non-portable constructs that JavaScript's native `RegExp` would accept.
- Deny-words are matched case-sensitively, like `strings.Contains`.

## Honest limits

Pattern rules match the text of a query and cannot parse SQL. A rule that passes its
test bench can still be evaded by a rewritten query. Warden makes pattern rules faster
to author and test; it does not make guardrails evasion-proof, and it says so in the UI.

## Development

```bash
make build     # go build → ./warden (CLI only, no npm)
make test      # go test ./...
```

Layout: `pkg/rules` is the rule-file schema, `pkg/match` the gateway-parity matcher,
`pkg/generate` the LLM generate-validate loop, `pkg/scan` the Alcatraz wiring, and
`cmd/warden` the CLI. The builder UI lives in the hoop.dev landing-page repo and is
served at [hoop.dev/labs/warden](https://hoop.dev/labs/warden).
