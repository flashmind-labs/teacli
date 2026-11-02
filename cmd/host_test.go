package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rayfish/teacli/modules/errors"
	"github.com/spf13/cobra"
)

func TestActionsEnvironment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/example/project" || r.Header.Get("Authorization") != "token job-token" {
			t.Errorf("unexpected request: %s, authorization present: %t", r.URL.Path, r.Header.Get("Authorization") != "")
		}
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	setHostConfig(t, "[other]\nurl = https://other.example.com\ntoken = saved-token\n", "")
	t.Setenv("GITHUB_SERVER_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "job-token")
	t.Setenv("GITHUB_REPOSITORY", "example/project")
	t.Setenv("TEACLI_TARGET_REPO", "")
	detected, err := detectCurrentRepo()
	if err != nil || detected.Owner != "example" || detected.Repo != "project" {
		t.Fatalf("repository detection = %+v, %v", detected, err)
	}
	resp, err := apiDo(&cobra.Command{}, http.MethodGet, "repos/"+detected.Owner+"/"+detected.Repo, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	hostName = "other"
	server, err := getServer(&cobra.Command{})
	if err != nil || server.URL != "https://other.example.com" || server.Token != "saved-token" {
		t.Fatalf("explicit server was not selected: %v", err)
	}
}

func TestActionsEnvironmentRejectsInvalidValues(t *testing.T) {
	setHostConfig(t, "", "")
	t.Setenv("GITHUB_TOKEN", "job-token")
	t.Setenv("GITHUB_SERVER_URL", "invalid")
	if _, err := getServer(&cobra.Command{}); errors.ExitCode(err) != errors.ExitValidationError {
		t.Fatalf("invalid URL: %v", err)
	}
	t.Setenv("GITHUB_SERVER_URL", "https://forge.example.com")
	t.Setenv("GITHUB_REPOSITORY", "invalid")
	t.Setenv("TEACLI_TARGET_REPO", "")
	if _, err := detectCurrentRepo(); errors.ExitCode(err) != errors.ExitValidationError {
		t.Fatalf("invalid repository: %v", err)
	}
}

func TestHostSelection(t *testing.T) {
	const one = "[primary]\nurl = https://forge.example.com\n"
	const two = one + "[secondary]\nurl = https://other.example.com\n"
	tests := []struct {
		name    string
		config  string
		host    string
		want    string
		message string
	}{
		{name: "single instance", config: one, want: "https://forge.example.com"},
		{name: "no instances", message: "specify --host"},
		{name: "multiple instances", config: two, message: "specify --host"},
		{name: "default does not disambiguate", config: "[global]\ndefault_server = primary\n" + two, message: "specify --host"},
		{name: "explicit name", config: two, host: "secondary", want: "https://other.example.com"},
		{name: "explicit hostname", config: two, host: "other.example.com", want: "https://other.example.com"},
		{name: "explicit URL", config: two, host: "https://other.example.com/", want: "https://other.example.com"},
		{name: "unknown host", config: two, host: "missing", message: "unknown host"},
		{name: "ambiguous hostname", config: one + "[secondary]\nurl = https://forge.example.com/subpath\n", host: "forge.example.com", message: "matches multiple instances"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setHostConfig(t, tt.config, tt.host)
			server, err := getServer(&cobra.Command{})
			if tt.message != "" {
				if errors.ExitCode(err) != errors.ExitValidationError || !strings.Contains(err.Error(), tt.message) {
					t.Fatalf("getServer error = %v, want validation error containing %q", err, tt.message)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if server.URL != tt.want {
				t.Fatalf("server URL = %q, want %q", server.URL, tt.want)
			}
		})
	}
}

func setHostConfig(t *testing.T, body, host string) {
	t.Helper()
	previousPath, previousHost, previousServer := configPath, hostName, serverName
	t.Cleanup(func() { configPath, hostName, serverName = previousPath, previousHost, previousServer })
	configPath = filepath.Join(t.TempDir(), "config.ini")
	hostName = host
	serverName = ""
	if err := os.WriteFile(configPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryDetectionSelectsServerFromRemote(t *testing.T) {
	t.Setenv("TEACLI_TARGET_REPO", "")
	setHostConfig(t, "[global]\ndefault_server = primary\n[primary]\nurl = https://forge.example.com\n[secondary]\nurl = https://other.example.com\n", "")
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", dir},
		{"-C", dir, "remote", "add", "origin", "https://forge.example.com/acme/widgets.git"},
		{"-C", dir, "remote", "add", "upstream", "https://other.example.com/acme/other.git"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Error(err)
		}
	})
	detected, err := detectCurrentRepo()
	if err != nil || detected.Server != "primary" || detected.Repo != "widgets" {
		t.Fatalf("automatic detection = %+v, %v", detected, err)
	}
	server, err := getServer(&cobra.Command{})
	if err != nil || server.URL != "https://forge.example.com" {
		t.Fatalf("automatic server = %+v, %v", server, err)
	}
	t.Setenv("TEACLI_TARGET_REPO", "acme/override")
	detected, err = detectCurrentRepo()
	if err != nil || detected.Server != "primary" || detected.Repo != "override" {
		t.Fatalf("environment override = %+v, %v", detected, err)
	}
	t.Setenv("TEACLI_TARGET_REPO", "")
	hostName = "secondary"
	detected, err = detectCurrentRepo()
	if err != nil {
		t.Fatal(err)
	}
	if detected.Server != "secondary" || detected.Repo != "other" {
		t.Fatalf("detected = %+v, want repository from selected instance", detected)
	}
}

func TestHostAndLegacyServerSelectors(t *testing.T) {
	setHostConfig(t, "[primary]\nurl = https://forge.example.com\n[secondary]\nurl = https://other.example.com\n", "")
	serverName = "secondary"
	server, err := getServer(&cobra.Command{})
	if err != nil || server.URL != "https://other.example.com" {
		t.Fatalf("legacy selector = %v, %v", server, err)
	}
	hostName = "primary"
	if _, err := getServer(&cobra.Command{}); errors.ExitCode(err) != errors.ExitValidationError {
		t.Fatalf("conflicting selectors: %v", err)
	}
}

func TestEnvironmentRepositoryRequiresHostWithMultipleInstances(t *testing.T) {
	setHostConfig(t, "[primary]\nurl = https://forge.example.com\n[secondary]\nurl = https://other.example.com\n", "")
	t.Setenv("TEACLI_TARGET_REPO", "acme/widgets")
	if _, err := detectCurrentRepo(); errors.ExitCode(err) != errors.ExitValidationError {
		t.Fatalf("missing host: %v", err)
	}
	hostName = "secondary"
	detected, err := detectCurrentRepo()
	if err != nil {
		t.Fatal(err)
	}
	if detected.Server != "secondary" || detected.Owner != "acme" || detected.Repo != "widgets" {
		t.Fatalf("unexpected repository: %+v", detected)
	}
}

func TestLogoutRequiresHostWithMultipleInstances(t *testing.T) {
	setHostConfig(t, "[global]\ndefault_server = primary\n[primary]\nurl = https://forge.example.com\n[secondary]\nurl = https://other.example.com\n", "")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := runAuthLogout(authTestCommand(false), nil); errors.ExitCode(err) != errors.ExitValidationError {
		t.Fatalf("missing host: %v", err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("logout without a host changed config")
	}
}
