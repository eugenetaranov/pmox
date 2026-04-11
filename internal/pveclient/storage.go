package pveclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
)

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

// UploadSnippet issues POST /nodes/{node}/storage/{storage}/upload with
// content=snippets and a multipart body carrying filename and file
// bytes. Used by `pmox create-template` to push the qemu-guest-agent
// bake snippet to the snippets storage before creating the VM.
func (c *Client) UploadSnippet(ctx context.Context, node, storage, filename string, content []byte) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("content", "snippets"); err != nil {
		return fmt.Errorf("write content field: %w", err)
	}
	fw, err := mw.CreateFormFile("filename", filename)
	if err != nil {
		return fmt.Errorf("create file part: %w", err)
	}
	if _, err := fw.Write(content); err != nil {
		return fmt.Errorf("write file bytes: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("close multipart: %w", err)
	}

	path := fmt.Sprintf("/nodes/%s/storage/%s/upload", node, storage)
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+path, &buf)
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

	respBody, _ := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", ErrUnauthorized, resp.Status)
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, resp.Status)
	case resp.StatusCode >= 400:
		return fmt.Errorf("%w: %s: %s", ErrAPIError, resp.Status, summarizeBody(respBody))
	}
	return nil
}

// UpdateStorageContent issues PUT /storage/{storage} with the given
// comma-joined content list. Cluster-wide endpoint, not per-node.
// Used to append `snippets` to a dir-capable storage during
// `pmox create-template`.
func (c *Client) UpdateStorageContent(ctx context.Context, storage, content string) error {
	form := url.Values{}
	form.Set("content", content)
	path := fmt.Sprintf("/storage/%s", storage)
	_, err := c.requestForm(ctx, "PUT", path, form)
	return err
}
