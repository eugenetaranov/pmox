## 1. Category model + selection

- [x] 1.1 Introduce a category descriptor (key, title, destructive, local) and tag each `cleanupItem`'s category against it
- [x] 1.2 Compute available categories (those with ≥1 item) and resolve the selected set: `--only` (exact) / `--skip` (default minus) / `--include-templates`; validate unknown keys → ErrUserInput
- [x] 1.3 Filter collected items by the selected set before report/apply
- [x] 1.4 Tests: only/skip/include-templates resolution, unknown-key error, template never in default set

## 2. Interactive checklist

- [x] 2.1 Add a `tui` multi-select that honors pre-checked options (huh `Option.Selected`)
- [x] 2.2 Behind a function seam, show the checklist on a TTY when no selection flags / `--no-input` / `--output json`; pre-check non-destructive, leave `template` unchecked; empty selection = no-op
- [x] 2.3 Tests: seam-driven selection scopes items; unchecking all removes nothing

## 3. New local collectors

- [x] 3.1 `cloud-init`: flag `CloudInitDir()/<slug>.yaml` files whose server is absent from config (derive slug via the same path code)
- [x] 3.2 `tack-profile`: flag profile entries whose server is gone or whose VMID is absent from the per-context VM set (skip contexts that couldn't be listed)
- [x] 3.3 `secret`: when the file backend is active, flag secrets.yaml URL entries absent from config; keychain backend → skip with a note
- [x] 3.4 Tests: cloud-init orphan detection, tack-profile staleness, file-secret orphan; keychain path is a no-op

## 4. Template category

- [x] 4.1 Per context, identify pmox templates (`template==1` + `-pmox-` name + VMID 9000–9099) and build a destructive delete item (VM destroy)
- [x] 4.2 Ensure template items are excluded from the default set and only removed under `--apply`
- [x] 4.3 Tests: identification (positive + non-pmox-in-range negative), default-exclusion, apply-gating

## 5. Wiring, docs, validation

- [x] 5.1 Add `--only`, `--skip`, `--include-templates` flags; update the command long help and category report titles
- [x] 5.2 README + llms.txt: document the new categories, template opt-in/destructiveness, the checklist, and the keychain-not-enumerable note
- [x] 5.3 `openspec validate cleanup-selectable --strict`, `gofmt`, `golangci-lint run ./...`, `go test -race ./...`, doccheck
