// Command doccheck validates relative links in pmox's documentation
// surface. It walks README.md, llms.txt, docs/*.md, and
// examples/README.md; extracts every [text](path) where path does not
// start with "http"; and asserts each target resolves relative to the
// containing file. Exits 0 on a clean walk, 1 on any miss.
//
// The tool exists as the offline fallback for `make docs-check` when
// lychee is not on PATH. Scope is deliberately narrow: no network
// fetches, no anchor checking, no image validation.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var linkRE = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "doccheck:", err)
		os.Exit(1)
	}
	files, err := gather(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "doccheck:", err)
		os.Exit(1)
	}
	misses := check(root, files)
	if len(misses) > 0 {
		for _, m := range misses {
			fmt.Fprintln(os.Stderr, m)
		}
		os.Exit(1)
	}
}

// gather returns the doc files we validate. Missing files are silently
// skipped so the tool is safe to run on partial trees during CI bring-up.
func gather(root string) ([]string, error) {
	out := []string{}
	candidates := []string{
		filepath.Join(root, "README.md"),
		filepath.Join(root, "llms.txt"),
		filepath.Join(root, "examples", "README.md"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	docsDir := filepath.Join(root, "docs")
	entries, err := os.ReadDir(docsDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		out = append(out, filepath.Join(docsDir, e.Name()))
	}
	return out, nil
}

// check returns a slice of "file:N: link -> target not found" messages.
func check(root string, files []string) []string {
	var misses []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			misses = append(misses, fmt.Sprintf("%s: %v", rel(root, f), err))
			continue
		}
		for _, m := range linkMatches(data) {
			target := m.target
			if isExternal(target) {
				continue
			}
			resolved := resolveTarget(f, target)
			if _, err := os.Stat(resolved); err != nil {
				misses = append(misses, fmt.Sprintf("%s:%d: %s -> %s not found",
					rel(root, f), m.line, target, rel(root, resolved)))
			}
		}
	}
	return misses
}

type match struct {
	target string
	line   int
}

func linkMatches(data []byte) []match {
	var out []match
	line := 1
	lineStart := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			slice := data[lineStart:i]
			for _, hit := range linkRE.FindAllSubmatch(slice, -1) {
				out = append(out, match{target: string(hit[1]), line: line})
			}
			line++
			lineStart = i + 1
		}
	}
	// Tail (no trailing newline)
	if lineStart < len(data) {
		slice := data[lineStart:]
		for _, hit := range linkRE.FindAllSubmatch(slice, -1) {
			out = append(out, match{target: string(hit[1]), line: line})
		}
	}
	return out
}

func isExternal(target string) bool {
	if len(target) == 0 {
		return true
	}
	if target[0] == '#' {
		return true
	}
	if len(target) >= 4 && (target[:4] == "http" || target[:4] == "mail") {
		return true
	}
	return false
}

func resolveTarget(srcFile, target string) string {
	// Strip fragment so docs/page.md#section resolves to docs/page.md.
	for i, c := range target {
		if c == '#' {
			target = target[:i]
			break
		}
	}
	if filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(filepath.Dir(srcFile), target)
}

func rel(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return r
}
