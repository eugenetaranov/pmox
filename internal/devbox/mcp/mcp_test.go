package mcp

import (
	"context"
	"reflect"
	"testing"
)

func TestLoadShippedSpecs(t *testing.T) {
	dirs := []string{"../../bootstrap/assets/mcp-catalog", "../../bootstrap/conf/mcp-catalog"}
	names := Names(dirs)
	for _, want := range []string{"github", "jira", "linear", "context7"} {
		found := false
		for _, n := range names {
			found = found || n == want
		}
		if !found {
			t.Errorf("no %s in %v", want, names)
		}
	}
	jira, err := Load(context.Background(), dirs, "jira")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(jira.Keys, []string{"JIRA_URL", "JIRA_USERNAME", "JIRA_API_TOKEN"}) || len(jira.Optional) == 0 || jira.Description == "" {
		t.Errorf("jira = %+v", jira)
	}
	if !IsSecret("JIRA_API_TOKEN") || IsSecret("JIRA_URL") || !IsSecret("LINEAR_API_KEY") {
		t.Error("IsSecret")
	}
}
