---
name: feature-map
description: Route work on Oregon Dev Foundry contact, authentication, one-time checkout, subscriptions, and admin catalog features to validated Go source anchors, behavioral invariants, and focused verification. Use before locating, explaining, changing, debugging, or verifying those user-facing flows.
---

# Oregon Dev Foundry feature map

Use this project-local map to orient feature work without repeating broad repository discovery. Detailed entries live under `features/` and are read on demand.

## Authority rule

The map is a routing cache, not architecture truth. Current repository instructions, current source, and observed runtime behavior always win. Validate every stored anchor before relying on it.

## Workflow

1. Read `skill://feature-map/features/README.md`.
2. Match the request to the closest feature ID from its user intent and entry points.
3. Read only that feature file initially.
4. Confirm each listed `path::symbol` exists and has the stated role. Use LSP when available; otherwise use scoped grep and AST search.
5. Expand definitions, implementations, references, and adjacent tests from those seeds before editing. The listed path is intentionally not a complete call graph.
6. If coverage is missing or an anchor is stale, fall back to current-source discovery and continue. Never force the request into a nearby feature.
7. After behavioral verification passes, update the matching feature file in the same change only when its user contract, entry points, primary anchors, effects, or verification path changed.

## Reading discipline

- Prefer authored `.templ` files over generated `*_templ.go` sources.
- Do not read every feature file speculatively.
- Do not treat line numbers, generated code, memory, or old session summaries as source authority.
- Do not block product work on map drift; treat it as a cache miss and repair the map after proving the current path.
- Keep new entries user-oriented and limited to 1–3 high-value source anchors.
