# QA risk hotspots

## CLI exit codes

- **Unknown command exits 0 while the root has no subcommands** — with no command registered, cobra treats any argument as input to the root and prints help with exit 0 (`orbit bogus`). Once the first slice is registered, check that `orbit bogus` exits 1 with a suggestion. *Seen while testing PR #19 — documented in the PR Notes.*
