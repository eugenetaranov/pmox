package pveclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// GetStoragePath calls GET /storage/{storage} and returns the on-disk
// "path" field — the mountpoint the storage is rooted at. Block-only
// storages (e.g. lvm, zfspool) return an empty path; callers should
// treat that as an error.
func (c *Client) GetStoragePath(ctx context.Context, storage string) (string, error) {
	body, err := c.request(ctx, "GET", "/storage/"+storage, nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		Data struct {
			Path string `json:"path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse storage response: %w", err)
	}
	if resp.Data.Path == "" {
		return "", fmt.Errorf("storage %s has no on-disk path (block-only storage cannot hold snippets)", storage)
	}
	return resp.Data.Path, nil
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
	path := fmt.Sprintf("/nodes/%s/storage/%s/download-url", node, storage)
	body, err := c.requestForm(ctx, "POST", path, form)
	if err != nil {
		return "", err
	}
	return parseDataString(body)
}

