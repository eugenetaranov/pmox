package router

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// fakeAPI is an in-memory PVE for the router's tests.
type fakeAPI struct {
	resources  []pveclient.Resource
	storage    []pveclient.Storage
	content    []pveclient.StorageContent
	apps       []pveclient.Appliance
	bridges    []pveclient.Bridge
	config     map[string]string
	firewallOn bool
	nextID     int
	ifaces     []pveclient.LXCIface
	downloaded string
	created    map[string]string
	fwRules    []url.Values
	fwOptions  url.Values
	setConfig  url.Values
	calls      []string
	createErr  error
}

func (f *fakeAPI) call(s string) { f.calls = append(f.calls, s) }

func (f *fakeAPI) ClusterResources(context.Context, string) ([]pveclient.Resource, error) {
	return f.resources, nil
}
func (f *fakeAPI) NextID(context.Context) (int, error)                           { return f.nextID, nil }
func (f *fakeAPI) WaitTask(context.Context, string, string, time.Duration) error { return nil }
func (f *fakeAPI) ListStorage(context.Context, string) ([]pveclient.Storage, error) {
	return f.storage, nil
}
func (f *fakeAPI) ListBridges(context.Context, string) ([]pveclient.Bridge, error) {
	return f.bridges, nil
}
func (f *fakeAPI) AplInfo(context.Context, string) ([]pveclient.Appliance, error) { return f.apps, nil }
func (f *fakeAPI) ClusterFirewallEnabled(context.Context) (bool, error)           { return f.firewallOn, nil }
func (f *fakeAPI) LXCInterfaces(context.Context, string, int) ([]pveclient.LXCIface, error) {
	return f.ifaces, nil
}
func (f *fakeAPI) ListStorageContent(context.Context, string, string, string) ([]pveclient.StorageContent, error) {
	return f.content, nil
}
func (f *fakeAPI) DownloadAppliance(_ context.Context, _, storage, tmpl string) (string, error) {
	f.downloaded = storage + ":" + tmpl
	return "UPID:dl", nil
}
func (f *fakeAPI) CreateLXC(_ context.Context, _ string, vmid int, kv map[string]string) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	f.created = kv
	f.call("create")
	return "UPID:create", nil
}
func (f *fakeAPI) LXCStart(context.Context, string, int) (string, error) {
	f.call("start")
	return "UPID:start", nil
}
func (f *fakeAPI) LXCStop(context.Context, string, int) (string, error) {
	f.call("stop")
	return "UPID:stop", nil
}
func (f *fakeAPI) DeleteLXC(context.Context, string, int) (string, error) {
	f.call("destroy")
	return "UPID:destroy", nil
}
func (f *fakeAPI) LXCStatus(context.Context, string, int) (*pveclient.VMStatus, error) {
	return &pveclient.VMStatus{Status: "running"}, nil
}
func (f *fakeAPI) LXCConfig(context.Context, string, int) (map[string]string, error) {
	return f.config, nil
}
func (f *fakeAPI) SetLXCConfig(_ context.Context, _ string, _ int, form url.Values) error {
	f.setConfig = form
	f.call("setconfig")
	return nil
}
func (f *fakeAPI) SetLXCFirewallOptions(_ context.Context, _ string, _ int, form url.Values) error {
	f.fwOptions = form
	return nil
}
func (f *fakeAPI) AddLXCFirewallRule(_ context.Context, _ string, _ int, form url.Values) error {
	f.fwRules = append(f.fwRules, form)
	return nil
}
func (f *fakeAPI) LXCFirewallOptions(context.Context, string, int) (map[string]string, error) {
	return map[string]string{"enable": f.fwOptions.Get("enable")}, nil
}

// fakeRun records node commands and their stdin, and answers state
// queries with stateOut.
type fakeRun struct {
	commands []string
	stdins   []string
	stateOut string
	err      error
}

func (r *fakeRun) run(_ context.Context, command string, stdin io.Reader, stdout, _ io.Writer) error {
	r.commands = append(r.commands, command)
	in := ""
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		in = string(b)
	}
	r.stdins = append(r.stdins, in)
	if r.err != nil {
		return r.err
	}
	if strings.Contains(command, "tailscale status --json") && stdout != nil {
		_, _ = io.Copy(stdout, bytes.NewBufferString(r.stateOut))
	}
	return nil
}

const runningStateOut = `{"BackendState":"Running","Self":{"TailscaleIPs":["100.81.31.119","fd7a:115c:a1e0::d435:1f78"],
"AllowedIPs":["100.81.31.119/32","fd7a:115c:a1e0::d435:1f78/128","192.168.0.185/32"],
"PrimaryRoutes":["192.168.0.185/32"],"Online":true}}
---pmox---
{"AdvertiseRoutes":["192.168.0.185/32","192.168.0.171/32"]}`
