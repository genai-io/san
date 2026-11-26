package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectConnectionSelection(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	project, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".san")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"projects": map[string]string{project: "client"}, "connections": map[string]any{"client": map[string]any{"endpointEnv": "JIRA_BASE_URL", "url": "https://client.example", "credentials": map[string]string{"JIRA_API_TOKEN": "CLIENT_JIRA_TOKEN"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "connections.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JIRA_BASE_URL", "https://work.example")
	t.Setenv("JIRA_API_TOKEN", "work")
	t.Setenv("CLIENT_JIRA_TOKEN", "")
	if err := applyProjectConnection(home, home); err != nil {
		t.Fatal(err)
	}
	if err := applyProjectConnection(home, project); err == nil {
		t.Fatal("missing project credential accepted")
	}
	if os.Getenv("JIRA_BASE_URL") != "https://work.example" || os.Getenv("JIRA_API_TOKEN") != "work" {
		t.Fatal("changed inherited connection without a complete selection")
	}
	t.Setenv("CLIENT_JIRA_TOKEN", "client")
	if err := applyProjectConnection(home, project); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("JIRA_BASE_URL") != "https://client.example" || os.Getenv("JIRA_API_TOKEN") != "client" {
		t.Fatal("project connection did not override inherited pair")
	}
}
