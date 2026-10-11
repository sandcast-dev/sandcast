# Brainstorm: sandcast website (landing page plus Antora docs)

**Date:** 2026-10-11
**Status:** active

## Problem Framing

sandcast has shipped on `main` (private repo `rhuss/sandcast`) with a full CLI, skill bundle and a set of Markdown docs under `docs/`. It needs a public home: a branded GitHub Pages site that explains why sandcast exists, shows what it does, and serves the complete documentation. A finished brand kit exists (`sandcast-brand-kit.zip`), and a sibling project already has a comparable site at `https://antwort-dev.github.io/` to mirror.

The opening premise was to create a `sandcast` GitHub org and move the project there. Investigation changed that premise:

- The `sandcast` GitHub namespace is already taken by an existing organization (display name "answerpad.io"); the `rhuss` account has no access to it. It cannot be used.
- `sandcast-dev` is available and mirrors the `antwort-dev` naming convention, giving a root-domain site at `https://sandcast-dev.github.io`.
- `rhuss/rhuss.github.io` already exists, so a personal-account site for sandcast could only be a subpath, not a root domain.

The brand kit is a complete Astro handoff: twelve logo variants, a transparent sandcastle hero, twenty icons, self-hosted Sora and Source Sans 3 fonts, design tokens, a seven-section landing-page copy document, a theme, and a product-facts document with explicit wording boundaries. The reference site is a landing page (Astro) plus a separate, versioned documentation section built with Antora (AsciiDoc; `modules/ROOT/pages/`; version `0.1`).

## Approaches Considered

### Placement of code and site

**A: New `sandcast-dev` org, move both (chosen).** Create a `sandcast-dev` org, transfer `rhuss/sandcast` to `sandcast-dev/sandcast`, and add `sandcast-dev/sandcast-dev.github.io` for the site at the root domain. GitHub auto-redirects old `rhuss/sandcast` URLs.
- Pros: clean mirror of antwort-dev; root-domain site; one home for code plus website; org avatar can use the brand logo.
- Cons: a repo transfer changes the canonical owner; the Go module path and brand-kit links reference `rhuss/sandcast` and need attention.

**B: `sandcast-dev` org, site only.** Keep code at `rhuss/sandcast`, create the org for the website repo alone.
- Pros: no repo move; brand-kit links stay literally correct.
- Cons: code and site split across two owners; less coherent.

**C: No org, personal subpath.** Keep code at `rhuss/sandcast`; host the site as `rhuss/sandcast-web` served at `rhuss.github.io/sandcast`.
- Pros: simplest; no org.
- Cons: subpath, not a root domain; does not mirror antwort-dev.

### Documentation engine

**Antora, mirroring antwort-dev (chosen).** Astro landing plus Antora versioned AsciiDoc docs. The reference site uses Antora, and the user's workflow already uses AsciiDoc (CLAUDE.md carries AsciiDoc conventions).
- Pros: matches the reference exactly; versioned docs suit a tool with pins and releases; docs-as-code with content sourced from the product repo.
- Cons: the existing docs are Markdown, so they need converting to `.adoc`; a combined Astro plus Antora build and deploy is more setup than a single framework.

**Starlight (Markdown-native).** Astro landing plus Astro Starlight docs.
- Pros: reuses the Markdown docs nearly as-is; built-in search and sidebar; a single Astro build.
- Cons: deviates from antwort-dev; brand styling needs CSS token overrides into Starlight.

**Custom Astro pages.** One Astro project for landing and docs.
- Pros: maximum brand consistency.
- Cons: hand-built sidebar, search and versioning; most bespoke effort.

### v1 content scope

**Full set now (chosen)** against "core now, stubs later" and "landing-first". The user wants the complete site on first launch, with demos and the recording as marked placeholders because no real assets exist yet.

## Decision

Build the sandcast website by mirroring antwort-dev: an Astro landing page using the brand kit, plus a versioned Antora AsciiDoc documentation section, published together to GitHub Pages at the root domain of a new `sandcast-dev` organization.

- Create the `sandcast-dev` org; set its avatar from the brand kit app icon; add an org profile.
- Transfer `rhuss/sandcast` to `sandcast-dev/sandcast`.
- Convert `docs/*.md` to an Antora module of AsciiDoc pages (one sentence per line) in the code repo, versioned as component `sandcast` at `0.1`.
- Create `sandcast-dev/sandcast-dev.github.io`: the Astro landing from the brand kit plus an Antora playbook that sources the docs from the code repo, with a CI workflow that builds both and deploys the combined output to Pages.
- Populate the full v1 content; ship demos and the recording as clearly marked placeholders.

## Key Requirements

- Dedicated `sandcast-dev` org, brand-kit avatar, org profile README linking to the site, the code, and OpenShell.
- Repo transfer `rhuss/sandcast` -> `sandcast-dev/sandcast`, relying on GitHub redirects; resolve the Go module path question (keep `github.com/rhuss/sandcast` via redirect, or rename to `github.com/sandcast-dev/sandcast` and update imports); update brand-kit copy links from `rhuss/sandcast` to the new owner.
- Website repo with two build systems: Astro (landing, brand kit assets, self-hosted fonts, design tokens) and Antora (versioned AsciiDoc docs), combined into one GitHub Pages deployment at the root domain.
- Landing page from the brand kit's seven sections (nav, hero "Build a sandbox. Make it yours.", three value statements, the capture-plus-build two halves, getting started, OpenShell explanation, footer) plus an explicit Why and use-case section, a Demos section, and a recording placeholder slot.
- Antora docs navigation: Overview and Why, Quickstart, Guides (capture, build and verify, profiles), Reference (CLI, configuration, pins), Example Walkthroughs (based on the reference-project flow of capture, lock, plan, build, verify and profile import that was validated in the walkthrough), and the OpenShell relation.
- Convert the six existing Markdown docs to AsciiDoc: `cli.md`, `configuration.md`, `pins.md`, `guides/capture.md`, `guides/build.md`, `guides/profiles.md`.
- Honor the brand kit's wording boundaries (`docs/PRODUCT-FACTS.md`): capture is `/sandcast-capture` inside Claude Code, not a terminal subcommand; no performance claims; no "fully isolated" or "guaranteed safe" language; profiles are imported and providers attached at runtime; prefer linking the live README over hard-coding versions.
- Accessibility and privacy: skip link, visible focus, correct alt text for the decorative hero, self-hosted fonts with no third-party font or CDN calls, measured contrast pairs.
- License footer: Apache License 2.0 (matches the repo).
- Clear links to GitHub (`sandcast-dev/sandcast`) and to OpenShell.

## Open Questions

- Go module path on transfer: keep `github.com/rhuss/sandcast` (works through GitHub's redirect) or rename to `github.com/sandcast-dev/sandcast` and update `go.mod` plus all imports and the embedded version pins.
- Whether the repo should also be made public at transfer time, or stay private until the site launches.
- Exact Antora versioning scheme: a single `0.1` component version now, and how it tracks the OpenShell pin and future sandcast releases.
- How the combined Astro plus Antora output is assembled and deployed (the antwort-dev CI workflow as the concrete template; landing at root, docs under `/docs/` with the versioned redirect).
- The recording placeholder mechanism (asciinema cast, an embedded video, or an animated terminal) and where the eventual demo assets will come from.
- Whether a custom domain is wanted later, or `sandcast-dev.github.io` is the permanent canonical origin.
- Whether to vendor the brand kit into the website repo or keep it as an external input.
