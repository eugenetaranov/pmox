package tack

import (
	"reflect"
	"testing"
)

func TestArgsMinimal(t *testing.T) {
	got := Args(Options{Playbook: "pb.yaml", User: "ubuntu", IP: "10.0.0.5", KeyPath: "/k/id"})
	want := []string{"run", "pb.yaml", "-c", "ssh://ubuntu@10.0.0.5", "--ssh-key", "/k/id"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args = %v\nwant  %v", got, want)
	}
}

func TestArgsFull(t *testing.T) {
	got := Args(Options{
		Playbook:    "pb.yaml",
		User:        "root",
		IP:          "192.168.0.9",
		Port:        2222,
		KeyPath:     "/k/id",
		Insecure:    true,
		Check:       true,
		AutoApprove: true,
		Tags:        []string{"docker", "net"},
		SkipTags:    []string{"slow"},
		OutputJSON:  true,
	})
	want := []string{
		"run", "pb.yaml",
		"-c", "ssh://root@192.168.0.9:2222",
		"--ssh-key", "/k/id",
		"--ssh-insecure",
		"--check",
		"--auto-approve",
		"--tags", "docker,net",
		"--skip-tags", "slow",
		"--output", "json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args = %v\nwant  %v", got, want)
	}
}

func TestArgsNoUserNoKey(t *testing.T) {
	got := Args(Options{Playbook: "pb.yaml", IP: "10.0.0.5"})
	want := []string{"run", "pb.yaml", "-c", "ssh://10.0.0.5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args = %v\nwant  %v", got, want)
	}
}
