# Brainstorm Overview

Last updated: 2026-10-11

## Sessions

| # | Date | Topic | Status | Spec | Issue |
|---|------|-------|--------|------|-------|
| 01 | 2026-10-08 | sandcast-standalone-build | spec-created | 001 | - |
| 02 | 2026-10-11 | website-landing-docs | active | - | - |

## Open Threads
- sandcast standalone build: profile id strategy for image-adapted copies, compiled policy fallback for gateways without profiles, refresh cadence for vendored upstream profiles and pinned mise, build plan format and stable API, fragment store lifecycle after promotion, portable skill format across OpenCode/Codex/pi, binary vs skill bundle version coupling, relationship to cc-deck profile delegation (from #01)
- sandcast website: Go module path handling on repo transfer, public-vs-private timing, Antora versioning scheme, combined Astro-plus-Antora deploy workflow, recording placeholder mechanism, custom domain later, vendoring the brand kit (from #02)

## Parked Ideas
- Pre-built layer stack images composed through FROM chains (#01)
  Reason: combinatorial image matrix and no real detection of what is missing. Fits later as a probed base image.

## Reference

`brainstorm/reference/` holds read-only copies of the cc-deck brainstorms that fed session 01. Their relative links point into the cc-deck repository and are not resolved here.
