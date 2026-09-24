package pveclient

import (
	"slices"
	"testing"
)

func TestStorageSupportsSnippets(t *testing.T) {
	if !(Storage{Content: "iso, snippets"}).SupportsSnippets() {
		t.Error("SupportsSnippets = false for 'iso, snippets'")
	}
	if (Storage{Content: "images,snippet"}).SupportsSnippets() {
		t.Error("SupportsSnippets false positive")
	}
}

func TestStorageContentList(t *testing.T) {
	cases := map[string][]string{
		"":                      nil,
		"   ":                   nil,
		"images":                {"images"},
		" iso, ,images,vztmpl ": {"iso", "images", "vztmpl"},
	}
	for in, want := range cases {
		if got := (Storage{Content: in}).ContentList(); !slices.Equal(got, want) {
			t.Errorf("ContentList(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilterStorage(t *testing.T) {
	pools := []Storage{
		{Storage: "a", Content: "images"},
		{Storage: "b", Content: "snippets"},
		{Storage: "c", Content: "iso,snippets"},
	}
	got := FilterStorage(pools, Storage.SupportsSnippets)
	if len(got) != 2 || got[0].Storage != "b" || got[1].Storage != "c" {
		t.Errorf("FilterStorage = %+v", got)
	}
	if got := FilterStorage(pools, func(Storage) bool { return false }); got != nil {
		t.Errorf("no match: got %+v, want nil", got)
	}
}
