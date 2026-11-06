package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestActionJobsResponseShapes(t *testing.T) {
	for _, shape := range []string{"object", "array"} {
		t.Run(shape, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/version":
					fmt.Fprint(w, `{"version":"1.25.0"}`)
				case "/api/v1/repos/owner/repo/actions/runs/42":
					fmt.Fprint(w, `{"id":42}`)
				case "/api/v1/repos/owner/repo/actions/runs/42/jobs":
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
			if !strings.Contains(string(data), "7") || !strings.Contains(string(data), "8") {
				t.Fatalf("output: %s", data)
			}
		})
	}
}
