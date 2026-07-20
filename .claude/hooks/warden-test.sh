#!/bin/sh
# Claude Code PostToolUse hook: after any file edit, if the file is a Warden
# rule file, run `warden test` on it. Exit 2 feeds the failures back to the
# agent so it fixes the rule before moving on.
set -u

file_path=$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("tool_input",{}).get("file_path",""))' 2>/dev/null)
[ -n "$file_path" ] || exit 0

case "$file_path" in
  *.yaml|*.yml) ;;
  *) exit 0 ;;
esac
[ -f "$file_path" ] || exit 0

# Only rule files: they carry both a rules list and examples.
grep -q '^rules:' "$file_path" && grep -q '^examples:' "$file_path" || exit 0

root=${CLAUDE_PROJECT_DIR:-.}
if [ -x "$root/warden" ]; then
  warden_bin=$root/warden
elif command -v warden >/dev/null 2>&1; then
  warden_bin=warden
else
  # No binary available; do not block the agent over tooling.
  exit 0
fi

output=$("$warden_bin" test "$file_path" 2>&1)
status=$?
if [ $status -ne 0 ]; then
  echo "warden test failed for $file_path; fix pattern_regex until every example passes:" >&2
  echo "$output" >&2
  exit 2
fi
exit 0
