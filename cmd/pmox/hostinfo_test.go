package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/eugenetaranov/pmox/internal/config"
	"github.com/eugenetaranov/pmox/internal/pvetest"
	"github.com/eugenetaranov/pmox/internal/server"
)

func TestHostInfo(t *testing.T) {
	f := pvetest.New(t)
	f.Handle("GET", "/version", pvetest.JSON(`{"data":{"version":"8.4.1"}}`))
	f.Handle("GET", "/storage", pvetest.JSON(`{"data":[
	  {"storage":"vm-data","type":"lvmthin","content":"images","active":1,"enabled":1,"avail":644245094400,"total":966367641600},
	  {"storage":"off","type":"nfs","content":"iso","active":0,"enabled":1,"avail":0,"total":0}]}`))
	f.HandleExact("GET", "/nodes", pvetest.JSON(`{"data":[
	  {"node":"p0","status":"online","cpu":0.234,"maxcpu":16,"mem":19541180416,"maxmem":67430641664,"uptime":1047600},
	  {"node":"p1","status":"offline"}]}`))
	f.Handle("GET", "/cluster/resources", pvetest.JSON(`{"data":[
	  {"vmid":100,"name":"text2cad","node":"p0","status":"running","tags":"pmox;pmox-ready"},
	  {"vmid":101,"name":"old","node":"p0","status":"stopped","tags":"pmox"},
	  {"vmid":102,"name":"other","node":"p0","status":"running"},
	  {"vmid":9000,"name":"tmpl","node":"p0","status":"stopped","template":1}]}`))
	info, err := collectHostInfo(context.Background(), f.Client(), &server.Resolved{URL: "https://pve:8006/api2/json", Server: &config.Server{Name: "prod"}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printHostInfo(&out, info)
	for _, want := range []string{
		"Context:  prod (https://pve:8006/api2/json)",
		"Proxmox:  VE 8.4.1",
		"Node:     p0 — online, up 12d 3h · CPU 16 cores, 23% used · memory 18.2 / 62.8 GiB",
		"  Storage: vm-data          600 GiB free of 900 (lvmthin)",
		"Node:     p1 — offline",
		"VMs:      2 pmox (1 running, 1 stopped) · 3 in all · 1 template",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "off ") {
		t.Errorf("an inactive pool is listed:\n%s", out.String())
	}
}
