package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.gitea.io/sdk/gitea"
	"github.com/spf13/cobra"
)

func TestActionJobsResponseShapes(t *testing.T) {
	for _, shape := range []string{"object", "array"} {
		t.Run(shape, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/version":
					fmt.Fprint(w, `{"version":"1.25.0"}`)
				case "/api/v1/repos/owner/repo/actions/runs/42":
					fmt.Fprint(w, `{"id":42}`)
				case "/api/v1/repos/owner/repo/actions/runs/42/jobs":
					requests++
					if shape == "array" {
						fmt.Fprint(w, `[{"id":7,"name":"build"},{"id":8,"name":"test"}]`)
						return
					}
					page := r.URL.Query().Get("page")
					if page == "3" {
						fmt.Fprint(w, `[]`)
						return
					}
					id := 7
					if page == "2" {
						id = 8
					}
					job := fmt.Sprintf(`{"id":%d,"name":"build","status":"running"}`, id)
					if shape == "array" {
						fmt.Fprintf(w, `[%s]`, job)
					} else {
						fmt.Fprintf(w, `{"total_count":2,"jobs":[%s]}`, job)
					}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			oldConfig, oldServer, oldAll := configPath, serverName, fetchAll
			configPath = filepath.Join(t.TempDir(), "config")
			serverName = "example"
			fetchAll = true
			defer func() { configPath, serverName, fetchAll = oldConfig, oldServer, oldAll }()
			if err := os.WriteFile(configPath, []byte("[example]\nurl = "+srv.URL+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := &cobra.Command{}
			cmd.Flags().Bool("dry-run", false, "")
			cmd.Flags().String("format", "text", "")
			cmd.Flags().Int("page", 0, "")
			cmd.Flags().Int("per-page", 0, "")
			out, err := os.CreateTemp(t.TempDir(), "out")
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			oldStdout := os.Stdout
			os.Stdout = out
			err = runActionJobs(cmd, []string{"owner/repo", "42"})
			os.Stdout = oldStdout
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(out.Name())
			if err != nil {
				t.Fatal(err)
			}
			if shape == "array" && requests != 1 {
				t.Fatalf("requested %d pages for an unpaginated array", requests)
			}
			if !strings.Contains(string(data), "7") || !strings.Contains(string(data), "8") {
				t.Fatalf("output: %s", data)
			}
		})
	}
}

func TestForgejoRunMetadataAndBranch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"workflow_runs":[]}`)
			return
		}
		fmt.Fprint(w, `{"workflow_runs":[{"id":42,"index_in_repo":9,"status":"running","workflow_id":"ci.yml","prettyref":"refs/heads/main","commit_sha":"abc","trigger_user":{"login":"user"},"started":"2024-01-01T00:00:00Z","updated":"2024-01-01T00:01:00Z"},{"id":43,"index_in_repo":10,"prettyref":"refs/heads/other"}]}`)
	}))
	defer srv.Close()
	oldConfig, oldServer := configPath, serverName
	configPath = filepath.Join(t.TempDir(), "config")
	serverName = "example"
	defer func() { configPath, serverName = oldConfig, oldServer }()
	if err := os.WriteFile(configPath, []byte("[example]\nurl = "+srv.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	runs, err := fetchActionRuns(cmd, "owner", "repo", gitea.ListRepoActionRunsOptions{Branch: "main"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != 42 || runs[0].RunNumber != 9 || runs[0].HeadBranch != "main" || runs[0].HeadSha != "abc" || runs[0].Actor == nil || runs[0].Actor.UserName != "user" || runs[0].StartedAt.IsZero() {
		t.Fatalf("runs: %+v", runs)
	}
}

func TestResolveForgejoRunNumber(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/version":
			fmt.Fprint(w, `{"version":"17.0.1"}`)
		case "/api/v1/repos/owner/repo/actions/runs/9":
			http.NotFound(w, r)
		case "/api/v1/repos/owner/repo/actions/runs":
			fmt.Fprint(w, `{"workflow_runs":[{"id":42,"index_in_repo":9}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldConfig, oldServer := configPath, serverName
	configPath = filepath.Join(t.TempDir(), "config")
	serverName = "example"
	defer func() { configPath, serverName = oldConfig, oldServer }()
	if err := os.WriteFile(configPath, []byte("[example]\nurl = "+srv.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	client, err := getClient(cmd)
	if err != nil {
		t.Fatal(err)
	}
	id, err := resolveActionRunID(cmd, client, "owner", "repo", "9")
	if err != nil || id != 42 {
		t.Fatalf("id=%d error=%v", id, err)
	}
}
