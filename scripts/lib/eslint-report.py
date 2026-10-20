"""Reads an eslint JSON report on stdin; exits 1 when it holds any problem.

Problems from the rule named by $RULE (the architectural rules) print first.
Used by scripts/lint-guards.sh; tested by scripts/lint-guards-test.sh.
"""
import json
import os
import sys

rule = os.environ["RULE"]
data = json.load(sys.stdin)

hits, other = [], []
for f in data:
    rel = "web/" + f["filePath"].split("/web/", 1)[-1]
    for m in f["messages"]:
        where = f"{rel}:{m.get('line', 0)}:{m.get('column', 0)}"
        entry = (where, m.get("ruleId") or "eslint", m["message"].split("\n", 1)[0])
        (hits if m.get("ruleId") == rule else other).append(entry)

for title, found in ((f"architectural lint violation(s) [{rule}]", hits), ("other eslint problem(s)", other)):
    if found:
        print(f"\n✗ {len(found)} {title}:\n")
        for where, rid, msg in found:
            print(f"  {where}  [{rid}]")
            print(f"    {msg}\n")

if hits or other:
    sys.exit(1)

print("✓ eslint: no problems")
