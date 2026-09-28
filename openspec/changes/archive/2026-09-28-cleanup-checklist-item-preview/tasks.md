## 1. Implement the preview

- [x] 1.1 Extract `printItemsByCategory` from `reportCleanup` into a shared helper
- [x] 1.2 Change `resolveSelection` to take `[]cleanupItem` (deriving counts internally) and a writer instead of a pre-computed `counts map[string]int`
- [x] 1.3 Print the preview (header + `printItemsByCategory`) inside `resolveSelection`'s interactive branch, before building checklist options, skipped when `len(items) == 0`
- [x] 1.4 Update `runCleanup`'s call site accordingly

## 2. Tests

- [x] 2.1 Update all existing `resolveSelection` test call sites for the new signature (added an `itemsFromCounts` test helper for count-only cases)
- [x] 2.2 Add a test asserting the preview contains full item detail for a populated category
- [x] 2.3 Add a test asserting no preview output when nothing was found
- [x] 2.4 Verify live via a pty-driven capture against real cluster state
- [x] 2.5 `go build ./...`, `go vet ./...`, `go test ./...`

## 3. Land the spec

- [x] 3.1 `openspec validate cleanup-checklist-item-preview --strict`
- [x] 3.2 Archive the change (merges into `openspec/specs/cleanup/spec.md`)
