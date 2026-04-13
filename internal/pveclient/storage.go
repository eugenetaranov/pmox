package pveclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
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

// PostSnippet uploads a snippet file to the given storage by issuing
// POST /nodes/{node}/storage/{storage}/upload as multipart/form-data.
// The endpoint is the only one in pveclient that uses multipart
// encoding rather than form-urlencoded — we stream the body via
// io.Pipe so the payload is not buffered twice.
func (c *Client) PostSnippet(ctx context.Context, node, storage, filename string, content []byte) error {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		var gerr error
		defer func() {
			_ = mw.Close()
			_ = pw.CloseWithError(gerr)
		}()
		if gerr = mw.WriteField("content", "snippets"); gerr != nil {
			return
		}
		if gerr = mw.WriteField("filename", filename); gerr != nil {
			return
		}
		fw, err := mw.CreateFormFile("file", filename)
		if err != nil {
			gerr = err
			return
		}
		if _, err := fw.Write(content); err != nil {
			gerr = err
			return
		}
	}()

	path := fmt.Sprintf("/nodes/%s/storage/%s/upload", node, storage)
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+path, pr)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", c.TokenID, c.Secret))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		if isTLSError(err) {
			return fmt.Errorf("%w: %w", ErrTLSVerificationFailed, err)
		}
		return fmt.Errorf("%w: %w", ErrNetwork, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", ErrUnauthorized, resp.Status)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, resp.Status)
	case resp.StatusCode >= 400:
		return fmt.Errorf("%w: %s: %s", ErrAPIError, resp.Status, summarizeBody(body))
	}
	return nil
}

// DeleteSnippet removes a snippet file from storage by issuing
// DELETE /nodes/{node}/storage/{storage}/content/{storage}:snippets/{filename}.
// 404 is mapped to ErrNotFound so callers can treat an already-missing
// file as success.
func (c *Client) DeleteSnippet(ctx context.Context, node, storage, filename string) error {
	path := fmt.Sprintf("/nodes/%s/storage/%s/content/%s:snippets/%s", node, storage, storage, filename)
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
	path := fmt.Sprintf("/nodes/%s/storage/%s/content", node, storage)
	body, err := c.request(ctx, "GET", path, q)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []StorageContent `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse storage content response: %w", err)
	}
	return resp.Data, nil
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

