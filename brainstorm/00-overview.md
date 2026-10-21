# Brainstorm Overview

Last updated: 2026-10-21

## Sessions

| # | Date | Topic | Status | Spec | Issue |
|---|------|-------|--------|------|-------|
| 01 | 2026-10-08 | sandcast-standalone-build | spec-created | 001 | - |
| 02 | 2026-10-11 | website-landing-docs | active | - | - |
| 03 | 2026-10-14 | runtime-home-shell-init | spec-created | 003 | - |

## Open Threads
- sandcast standalone build: profile id strategy for image-adapted copies, compiled policy fallback for gateways without profiles, refresh cadence for vendored upstream profiles and pinned mise, build plan format and stable API, fragment store lifecycle after promotion, portable skill format across OpenCode/Codex/pi, binary vs skill bundle version coupling, relationship to cc-deck profile delegation (from #01)
- sandcast website: Go module path handling on repo transfer, public-vs-private timing, Antora versioning scheme, combined Astro-plus-Antora deploy workflow, recording placeholder mechanism, custom domain later, vendoring the brand kit (from #02)
- runtime home and shell init: how bash avoids auto-sourcing a workspace `~/.bashrc` without a ZDOTDIR equivalent, verifying ZDOTDIR and XDG survive the overmount against the OpenShell base image, where relocation variables are set for every process, the curated dotfile allowlist, unifying with the existing shell-curation flow, and how treating a `/sandbox`-only probed tool as missing interacts with the probe cache and plan delta (from #03)

## Parked Ideas
- Pre-built layer stack images composed through FROM chains (#01)
  Reason: combinatorial image matrix and no real detection of what is missing. Fits later as a probed base image.

## Reference

`brainstorm/reference/` holds read-only copies of the cc-deck brainstorms that fed session 01. Their relative links point into the cc-deck repository and are not resolved here.
