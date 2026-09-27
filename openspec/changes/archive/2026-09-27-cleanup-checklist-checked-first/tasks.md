## 1. Fix and verify

- [x] 1.1 Change `resolveSelection`'s option ordering from two-tier (populated/empty) to three-tier (checked/populated-unchecked/empty)
- [x] 1.2 Verify live via a pty-driven capture of the real interactive checklist against real cluster state, confirming the previously-missing populated destructive category now renders and the checked category is at index 0
- [x] 1.3 Update the regression test in `cmd/pmox/cleanup_selectable_test.go` to cover a populated destructive category declared earlier than the only checked category
- [x] 1.4 `go build ./...`, `go vet ./...`, `go test ./...`

## 2. Land the spec correction

- [x] 2.1 `openspec validate cleanup-checklist-checked-first --strict`
- [x] 2.2 Archive the change (merges the corrected requirement into `openspec/specs/cleanup/spec.md`)
