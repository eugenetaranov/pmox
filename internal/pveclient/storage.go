package pveclient

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// StorageContent is one entry from GET /nodes/{node}/storage/{storage}/content.
type StorageContent struct {
	Volid  string `json:"volid"`
	Format string `json:"format"`
	Size   int64  `json:"size"`
}

// GetStoragePath calls GET /storage/{storage} and returns the on-disk
// "path" field — the mountpoint the storage is rooted at. Block-only
// storages (e.g. lvm, zfspool) return an empty path; callers should
// treat that as an error.
func (c *Client) GetStoragePath(ctx context.Context, storage string) (string, error) {
	data, err := getData[struct {
		Path string `json:"path"`
	}](ctx, c, "/storage/"+url.PathEscape(storage), nil, "storage response")
	if err != nil {
		return "", err
	}
	if data.Path == "" {
		return "", fmt.Errorf("storage %s has no on-disk path (block-only storage cannot hold snippets)", storage)
	}
	return data.Path, nil
}

// DeleteSnippet removes a snippet file from storage by issuing
// DELETE /nodes/{node}/storage/{storage}/content/{storage}:snippets/{filename}.
// 404 is mapped to ErrNotFound so callers can treat an already-missing
// file as success.
func (c *Client) DeleteSnippet(ctx context.Context, node, storage, filename string) error {
	// The volid's "snippets/" separator stays a literal slash (as PVE
	// expects); only the caller-supplied components are escaped.
	st := url.PathEscape(storage)
	path := fmt.Sprintf("/nodes/%s/storage/%s/content/%s:snippets/%s", url.PathEscape(node), st, st, url.PathEscape(filename))
	_, err := c.request(ctx, "DELETE", path, nil)
	return err
}

// ListStorageContent fetches the list of files present in a given
// content category via GET /nodes/{node}/storage/{storage}/content?content=<filter>.
func (c *Client) ListStorageContent(ctx context.Context, node, storage, contentFilter string) ([]StorageContent, error) {
	q := url.Values{}
	if contentFilter != "" {
		q.Set("content", contentFilter)
	}
	path := fmt.Sprintf("/nodes/%s/storage/%s/content", url.PathEscape(node), url.PathEscape(storage))
	return getData[[]StorageContent](ctx, c, path, q, "storage content response")
}

// UpdateStorageContent rewrites the `content` list of a cluster-wide
// storage entry by issuing PUT /storage/{storage} with a form body of
// `content=<comma-joined>`. The full new list is sent — PVE replaces
// the existing one. Used by `pmox init` to enable `snippets` on
// an existing dir-backed storage when no snippet-capable storage is
// present.
func (c *Client) UpdateStorageContent(ctx context.Context, storage string, content []string) error {
	form := url.Values{}
	form.Set("content", strings.Join(content, ","))
	_, err := c.requestForm(ctx, "PUT", "/storage/"+url.PathEscape(storage), form)
	return err
}

// DownloadURL issues POST /nodes/{node}/storage/{storage}/download-url,
// which asks PVE to fetch a URL (typically a cloud image) directly to
// the named storage. params holds the form fields (url, content,
// filename, checksum, checksum-algorithm, ...). Returns the UPID of
// the asynchronous download task.
func (c *Client) DownloadURL(ctx context.Context, node, storage string, params map[string]string) (string, error) {
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	path := fmt.Sprintf("/nodes/%s/storage/%s/download-url", url.PathEscape(node), url.PathEscape(storage))
	body, err := c.requestForm(ctx, "POST", path, form)
	if err != nil {
		return "", err
	}
	return parseDataString(body)
}
