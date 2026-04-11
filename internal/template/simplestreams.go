package template

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// defaultCatalogueURL points at Canonical's released cloud image
// simplestreams index. Stable for 10+ years and consumed by MAAS,
// Juju, and cloud-init itself.
const defaultCatalogueURL = "https://cloud-images.ubuntu.com/releases/streams/v1/com.ubuntu.cloud:released:download.json"

// mirrorBaseURL is the base joined with each item's `path` to form
// the concrete download URL.
const mirrorBaseURL = "https://cloud-images.ubuntu.com/"

// maxEntries caps how many candidate images the picker shows.
const maxEntries = 10

// ImageEntry is a single row in the picker.
type ImageEntry struct {
	Release     string // e.g. "24.04"
	Codename    string // e.g. "noble"
	VersionDate string // simplestreams version key, YYYYMMDD
	URL         string // fully-qualified download URL
	SHA256      string
	Label       string // human display label
	IsLTS       bool
}

// simplestreamsDoc is the minimal JSON shape we parse from the
// Canonical feed. We keep the decode narrow so a field rename
// upstream fails loudly instead of corrupting a picker entry.
type simplestreamsDoc struct {
	Products map[string]simplestreamsProduct `json:"products"`
}

type simplestreamsProduct struct {
	Release         string                          `json:"release"`
	ReleaseTitle    string                          `json:"release_title"`
	ReleaseCodename string                          `json:"release_codename"`
	Arch            string                          `json:"arch"`
	Aliases         string                          `json:"aliases"`
	Versions        map[string]simplestreamsVersion `json:"versions"`
}

type simplestreamsVersion struct {
	Items map[string]simplestreamsItem `json:"items"`
}

type simplestreamsItem struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	FType  string `json:"ftype"`
	Size   int64  `json:"size"`
}

// fetchCatalogue GETs the simplestreams index, filters to amd64
// disk1.img items, picks the newest version of each release, sorts
// newest-first, and caps at maxEntries.
func fetchCatalogue(ctx context.Context, catalogueURL string) ([]ImageEntry, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", catalogueURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", catalogueURL, err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", catalogueURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch %s: http %d", catalogueURL, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", catalogueURL, err)
	}
	var doc simplestreamsDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", catalogueURL, err)
	}

	var entries []ImageEntry
	for _, prod := range doc.Products {
		if prod.Arch != "amd64" {
			continue
		}
		versionKey, version, ok := latestVersion(prod.Versions)
		if !ok {
			continue
		}
		item, ok := version.Items["disk1.img"]
		if !ok {
			continue
		}
		codename := prod.ReleaseCodename
		if codename == "" {
			codename = prod.Release
		}
		release := prod.ReleaseTitle
		if release == "" {
			release = prod.Release
		}
		isLTS := strings.Contains(prod.Aliases, "lts")
		label := fmt.Sprintf("ubuntu %s (%s) %s", release, codename, versionKey)
		if isLTS {
			label += " LTS"
		}
		entries = append(entries, ImageEntry{
			Release:     release,
			Codename:    codename,
			VersionDate: versionKey,
			URL:         mirrorBaseURL + strings.TrimPrefix(item.Path, "/"),
			SHA256:      item.SHA256,
			Label:       label,
			IsLTS:       isLTS,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].VersionDate > entries[j].VersionDate
	})
	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
	}
	return entries, nil
}

// latestVersion picks the lexicographically largest key from the
// versions map — simplestreams keys are YYYYMMDD dates, so lex =
// chronological.
func latestVersion(versions map[string]simplestreamsVersion) (string, simplestreamsVersion, bool) {
	if len(versions) == 0 {
		return "", simplestreamsVersion{}, false
	}
	keys := make([]string, 0, len(versions))
	for k := range versions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	latest := keys[len(keys)-1]
	return latest, versions[latest], true
}

// defaultImageIndex returns the index of the latest LTS entry in a
// newest-first list, or 0 if there is no LTS in the list (or the
// list is empty).
func defaultImageIndex(entries []ImageEntry) int {
	for i, e := range entries {
		if e.IsLTS {
			return i
		}
	}
	return 0
}
