---
title: Validation severity filtering
description: Apply command input thresholds without weakening declaration floors or post-Handle controls.
---

An advisory input warning should not accidentally block a command, but a command's declared floor must not be weakened by a caller. The policy distinguishes input-stage allowance from returned Handle controls.

## Numeric contract

| Severity | Value |
| --- | --- |
| Unknown | 0 |
| Information | 1 |
| Warning | 2 |
| Error | 3 |

Without a caller allowance or command floor, only Error findings survive filters, input validation, and preparation. With `ExecuteOptions.AllowedSeverity`, only findings strictly greater than the allowance survive. Nonblocking findings are removed, not retained as successful diagnostics.

`WithBlockOnValidationSeverity[C]` is inclusive. The effective allowance cannot exceed the command floor minus one, and Unknown always blocks an attributed command. Callers may tighten a floor, never loosen it. Out-of-range configuration fails before callbacks.

## Preparation and returned controls differ

Preparation controls apply input severity before deciding whether Handle may run. `ProvidedWith` additionally needs a usable typed payload; `StopProviding` always stops even when its fragment is successful.

Do not apply that filter after Handle. A singular returned `validation.Result` warning remains in the result and makes it invalid. Explicit `Control` also preserves its findings. Completion failures are never weakened by input allowances. Bare validation collections are ordinary response payloads, not a hidden control grammar.

## Keep ingress separate

Backend allowances are trusted application input. The existing `validation.AllowedFromHeader` parser caps untrusted Error allowance at Warning and rejects malformed or out-of-range header values. Actual HTTP hosting remains a separate slice; the command pipeline does not read headers.
