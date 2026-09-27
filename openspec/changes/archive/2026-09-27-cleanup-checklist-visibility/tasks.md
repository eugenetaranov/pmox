## 1. Fix the checklist option ordering

- [x] 1.1 In `cmd/pmox/cleanup.go`'s `resolveSelection`, build the interactive checklist's option list sorted with populated categories (`counts[c.key] > 0`) first (stable, preserving `cleanupCategories`' relative order within each group), empty categories after
- [x] 1.2 Confirm this only affects the transient option list built for the picker — `cleanupCategories`' own declared order (used elsewhere: validation messages, report ordering) is untouched

## 2. Regression test

- [x] 2.1 Add a test in `cmd/pmox/cleanup_selectable_test.go` that seeds item counts so a populated category sits late in `cleanupCategories`' declared order, drives `resolveSelection` interactively via the `selectCategoriesFn` seam, and asserts the option list handed to the seam has every populated category before every empty one
- [x] 2.2 `go build ./...`, `go vet ./...`, `go test ./...`

## 3. Land the live spec

- [x] 3.1 `openspec validate cleanup-checklist-visibility --strict`
- [x] 3.2 Archive the change (merges `specs/cleanup/spec.md` into `openspec/specs/cleanup/spec.md`, establishing the capability live for the first time)
