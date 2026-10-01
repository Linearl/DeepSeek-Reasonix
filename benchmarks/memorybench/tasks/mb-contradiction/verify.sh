#!/usr/bin/env bash
set -e
# The negative match must not hit inside "pnpm install": the bare substring
# "npm install" occurs there (p-n-p-m), which made this task structurally
# unpassable (upstream #11333). Anchor "npm install" to a non-letter start.
grep -q "pnpm install" answer.txt && ! grep -qE "(^|[^A-Za-z])npm install" answer.txt
