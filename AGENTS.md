# AGENTS.md

<!-- scaffold: two or three sentences: what package `nexusmods` does, who imports it, and what it never
imports (no consumer package). -->

## Gate

`gate` is the command. Each override in `.gate.toml` is there because a runner
cannot resolve that check from PATH. The `windows-latest` test job is the only place
Windows-only behaviour runs; say here which tests depend on it.

## Shape

<!-- scaffold: the invariants a change must keep, each with its reason: what stays unexported and
why, which function is the single path for what, which bound is policy and which is not. -->

## Files

<!-- scaffold: a `| File | Holds |` table, one row per non-test Go file. -->
