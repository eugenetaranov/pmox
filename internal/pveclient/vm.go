package pveclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// VMStatus is the parsed /status/current response for a single VM.
type VMStatus struct {
	Status    string `json:"status"`    // "running" | "stopped"
	QMPStatus string `json:"qmpstatus"` // finer-grained status
	VMID      int    `json:"vmid"`
	Name      string `json:"name"`
	Uptime    int64  `json:"uptime"`
	CPUs      int    `json:"cpus"`
	Mem       int64  `json:"mem"`
	MaxMem    int64  `json:"maxmem"`
}

// Clone issues POST /nodes/{node}/qemu/{sourceID}/clone and returns the
// UPID of the asynchronous clone task. Always performs a full clone
// (full=1) so the new VM is independent of the source template.
func (c *Client) Clone(ctx context.Context, node string, sourceID, newID int, name string) (string, error) {
	form := url.Values{}
	form.Set("newid", strconv.Itoa(newID))
	form.Set("name", name)
	form.Set("full", "1")
	path := fmt.Sprintf("/nodes/%s/qemu/%d/clone", node, sourceID)
	body, err := c.requestForm(ctx, "POST", path, form)
	if err != nil {
		return "", err
	}
	return parseDataString(body)
}

// Resize issues PUT /nodes/{node}/qemu/{vmid}/resize to grow a VM disk.
// disk is typically "scsi0"; size is either "+NG" (grow by N GB) or
// "NG" (absolute new size).
func (c *Client) Resize(ctx context.Context, node string, vmid int, disk, size string) error {
	form := url.Values{}
	form.Set("disk", disk)
	form.Set("size", size)
	path := fmt.Sprintf("/nodes/%s/qemu/%d/resize", node, vmid)
	_, err := c.requestForm(ctx, "PUT", path, form)
	return err
}

// SetConfig issues POST /nodes/{node}/qemu/{vmid}/config with the given
// key/value pairs encoded as form values. Used to push cloud-init keys
// and resource settings.
//
// The PVE API has a quirk: the "sshkeys" value must itself be URL-
// encoded once *before* it's passed into form encoding, which then
// encodes it a second time. This method handles the inner encoding
// automatically when "sshkeys" is present in kv.
func (c *Client) SetConfig(ctx context.Context, node string, vmid int, kv map[string]string) error {
	form := url.Values{}
	for k, v := range kv {
		if k == "sshkeys" {
			// PVE requires sshkeys to be pre-URL-encoded with spaces as
			// %20, NOT as + — url.QueryEscape produces + for spaces
			// (application/x-www-form-urlencoded style), which PVE's
			// validator rejects as "invalid urlencoded string".
			v = strings.ReplaceAll(url.QueryEscape(v), "+", "%20")
		}
		form.Set(k, v)
	}
	path := fmt.Sprintf("/nodes/%s/qemu/%d/config", node, vmid)
	_, err := c.requestForm(ctx, "POST", path, form)
	return err
}

// Start issues POST /nodes/{node}/qemu/{vmid}/status/start and returns
// the UPID of the asynchronous start task.
func (c *Client) Start(ctx context.Context, node string, vmid int) (string, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/status/start", node, vmid)
	body, err := c.requestForm(ctx, "POST", path, nil)
	if err != nil {
		return "", err
	}
	return parseDataString(body)
}

// Shutdown issues POST /nodes/{node}/qemu/{vmid}/status/shutdown (the
// graceful, ACPI-driven power-off) and returns the UPID of the
// asynchronous task.
func (c *Client) Shutdown(ctx context.Context, node string, vmid int) (string, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/status/shutdown", node, vmid)
	body, err := c.requestForm(ctx, "POST", path, nil)
	if err != nil {
		return "", err
	}
	return parseDataString(body)
}

// Stop issues POST /nodes/{node}/qemu/{vmid}/status/stop (hard
// power-off, no guest cooperation) and returns the UPID of the
// asynchronous task.
func (c *Client) Stop(ctx context.Context, node string, vmid int) (string, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/status/stop", node, vmid)
	body, err := c.requestForm(ctx, "POST", path, nil)
	if err != nil {
		return "", err
	}
	return parseDataString(body)
}

// GetStatus issues GET /nodes/{node}/qemu/{vmid}/status/current and
// returns the parsed VMStatus block.
func (c *Client) GetStatus(ctx context.Context, node string, vmid int) (*VMStatus, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/status/current", node, vmid)
	body, err := c.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data VMStatus `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse status response: %w", err)
	}
	return &payload.Data, nil
}

// CreateVM issues POST /nodes/{node}/qemu to create a new VM from
// scratch. kv holds the free-form config keys (name, memory, cores,
// net0, scsi0, ide2, cicustom, agent, serial0, vga, scsihw, boot,
// ipconfig0, ...); vmid is added to the form automatically. Returns
// the UPID of the asynchronous create task.
//
// Used by `pmox create-template` to create the template-build VM
// with a `scsi0=<storage>:0,importfrom=<iso-storage>:iso/<file>`
// disk spec — the importfrom parameter (PVE 8.0+) turns a one-step
// API call into the equivalent of `qm create` + `qm importdisk`.
func (c *Client) CreateVM(ctx context.Context, node string, vmid int, kv map[string]string) (string, error) {
	form := url.Values{}
	form.Set("vmid", strconv.Itoa(vmid))
	for k, v := range kv {
		form.Set(k, v)
	}
	path := fmt.Sprintf("/nodes/%s/qemu", node)
	body, err := c.requestForm(ctx, "POST", path, form)
	if err != nil {
		return "", err
	}
	return parseDataString(body)
}

// ConvertToTemplate issues POST /nodes/{node}/qemu/{vmid}/template,
// flipping an existing stopped VM into a Proxmox template. The VM
// must be stopped; calling this on a running VM returns an API
// error from PVE.
func (c *Client) ConvertToTemplate(ctx context.Context, node string, vmid int) error {
	path := fmt.Sprintf("/nodes/%s/qemu/%d/template", node, vmid)
	_, err := c.requestForm(ctx, "POST", path, nil)
	return err
}

// Delete issues DELETE /nodes/{node}/qemu/{vmid} and returns the UPID
// of the asynchronous destroy task. The caller is responsible for
// stopping the VM first — deleting a running VM will fail at the PVE
// side.
func (c *Client) Delete(ctx context.Context, node string, vmid int) (string, error) {
	path := fmt.Sprintf("/nodes/%s/qemu/%d", node, vmid)
	body, err := c.request(ctx, "DELETE", path, nil)
	if err != nil {
		return "", err
	}
	return parseDataString(body)
}

// parseDataString extracts the string value from a {"data":"..."}
// envelope — the shape PVE uses to return UPID strings for async
// operations.
func parseDataString(body []byte) (string, error) {
	var payload struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parse data string response: %w", err)
	}
	return payload.Data, nil
}
