## 1. Spec hardening

- [x] 1.1 Verify the MODIFIED requirement in `openspec/changes/enforce-full-clone/specs/pveclient-core/spec.md` copies the full existing "Clone endpoint" requirement block from `openspec/specs/pveclient-core/spec.md` and only adds the full-clone constraint plus the new scenario.
- [x] 1.2 Run `openspec validate enforce-full-clone --strict` and fix any reported issues.

## 2. Code audit (no changes expected)

- [x] 2.1 Re-read `internal/pveclient/vm.go` `Clone` and confirm `form.Set("full", "1")` is unconditional and the function signature takes no `full` parameter. If either is false, this change has uncovered a real bug — stop and surface it.
- [x] 2.2 Confirm `TestClone_HappyPath` in `internal/pveclient/vm_test.go` still asserts `full=1` in the encoded form body. No new test is added.
- [x] 2.3 Grep the repo for any other caller of `Clone` (`rg "\.Clone\("`) and confirm none pass through a caller-controlled full flag.

## 3. Archive

- [ ] 3.1 After merge, run `openspec archive enforce-full-clone` so the tightened requirement lands in `openspec/specs/pveclient-core/spec.md`.
