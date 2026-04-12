package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eugenetaranov/pmox/internal/pvetest"
	"github.com/eugenetaranov/pmox/internal/vm"
)

const threeVMsListFixture = `{"data":[
  {"vmid":104,"name":"web1","node":"pve1","status":"running","tags":"pmox"},
  {"vmid":105,"name":"db1","node":"pve1","status":"stopped","tags":"pmox"},
  {"vmid":200,"name":"legacy","node":"pve2","status":"running","tags":""}
]}`

func newListFake(t *testing.T) *pvetest.Server {
	t.Helper()
	s := pvetest.New(t)
	s.Handle("GET", "/cluster/resources", pvetest.JSON(threeVMsListFixture))
	s.Handle("GET", "/agent/network-get-interfaces", func(w http.ResponseWriter, r *http.Request, _ string) {
		// web1 (vmid 104) on pve1 — real IP; legacy (200) on pve2 — blank via error.
		if strings.Contains(r.URL.Path, "/104/") {
			fmt.Fprint(w, `{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address-type":"ipv4","ip-address":"192.168.1.43"}]}]}}`)
			return
		}
		w.WriteHeader(500)
		fmt.Fprint(w, `{"errors":"agent not running"}`)
	})
	return s
}

func newTestListCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "list"}
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetContext(context.Background())
	return cmd, &out, &errb
}

func TestList_DefaultFiltersPMOX(t *testing.T) {
	f := newListFake(t)
	outputMode = "text"
	cmd, out, _ := newTestListCmd()
	if err := executeList(cmd.Context(), cmd, f.Client(), &listFlags{}); err != nil {
		t.Fatalf("executeList: %v", err)
	}
	body := out.String()
	if !strings.Contains(body, "web1") {
		t.Errorf("want web1 in output: %q", body)
	}
	if !strings.Contains(body, "db1") {
		t.Errorf("want db1 in output: %q", body)
	}
	if strings.Contains(body, "legacy") {
		t.Errorf("unexpected untagged VM in default output: %q", body)
	}
	// Running VM got its IP via AgentNetwork; stopped one renders blank.
	if !strings.Contains(body, "192.168.1.43") {
		t.Errorf("web1 IP missing: %q", body)
	}
}

func TestList_AllIncludesUntagged(t *testing.T) {
	f := newListFake(t)
	outputMode = "text"
	cmd, out, _ := newTestListCmd()
	if err := executeList(cmd.Context(), cmd, f.Client(), &listFlags{all: true}); err != nil {
		t.Fatalf("executeList: %v", err)
	}
	body := out.String()
	if !strings.Contains(body, "legacy") {
		t.Errorf("--all should include legacy: %q", body)
	}
}

func TestList_JSONOutput(t *testing.T) {
	f := newListFake(t)
	outputMode = "json"
	t.Cleanup(func() { outputMode = "text" })
	cmd, out, _ := newTestListCmd()
	if err := executeList(cmd.Context(), cmd, f.Client(), &listFlags{}); err != nil {
		t.Fatalf("executeList: %v", err)
	}
	var rows []vm.Row
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("json decode: %v — body=%q", err, out.String())
	}
	if len(rows) != 2 {
		t.Errorf("len(rows) = %d, want 2 (pmox-tagged only)", len(rows))
	}
	var web1 *vm.Row
	for i := range rows {
		if rows[i].Name == "web1" {
			web1 = &rows[i]
		}
	}
	if web1 == nil {
		t.Fatal("web1 not in JSON output")
	}
	if web1.IP != "192.168.1.43" {
		t.Errorf("web1.IP = %q", web1.IP)
	}
}
