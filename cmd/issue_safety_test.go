package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"

	"code.gitea.io/sdk/gitea"
	"github.com/flashmind-labs/teacli/modules/errors"
	"github.com/spf13/cobra"
)

func TestIssueStateRespectsDryRun(t *testing.T) {
	for name, run := range map[string]func(*cobra.Command, []string) error{"close": runIssueClose, "reopen": runIssueReopen} {
		t.Run(name, func(t *testing.T) {
			requests := 0
			cmd := safetyCommand(t, func(w http.ResponseWriter, r *http.Request) { requests++; fmt.Fprint(w, `{"number":1}`) })
			if err := cmd.Flags().Set("dry-run", "true"); err != nil {
				t.Fatal(err)
			}
			_, err := safetyOutput(t, func() error { return run(cmd, []string{"owner/repo", "1"}) })
			if err != nil {
				t.Fatal(err)
			}
			if requests != 0 {
				t.Errorf("dry run sent %d resource requests", requests)
			}
		})
	}
}

func TestIssueRejectsMalformedNumbers(t *testing.T) {
	tests := []struct {
		name string
		run  func(*cobra.Command, []string) error
		args []string
	}{
		{"get", runIssueGet, []string{"owner/repo", "12oops"}},
		{"close", runIssueClose, []string{"owner/repo", "12oops"}},
		{"reopen", runIssueReopen, []string{"owner/repo", "12oops"}},
		{"comments", runIssueCommentList, []string{"owner/repo", "12oops"}},
		{"comment create", runIssueCommentCreate, []string{"owner/repo", "12oops"}},
		{"comment delete", runIssueCommentDelete, []string{"owner/repo", "12oops"}},
		{"labels", runIssueLabelList, []string{"owner/repo", "12oops"}},
		{"label add", runIssueLabelAdd, []string{"owner/repo", "12oops", "1"}},
		{"label remove", runIssueLabelRemove, []string{"owner/repo", "12oops", "1"}},
		{"label clear", runIssueLabelClear, []string{"owner/repo", "12oops"}},
		{"assign", runIssueAssign, []string{"owner/repo", "12oops", "example"}},
		{"unassign", runIssueUnassign, []string{"owner/repo", "12oops", "example"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			cmd := safetyCommand(t, func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(http.StatusNotFound) })
			_, err := safetyOutput(t, func() error { return tt.run(cmd, tt.args) })
			if got := errors.ExitCode(err); got != 2 {
				t.Errorf("exit = %d, want 2 (error: %v)", got, err)
			}
			if requests != 0 {
				t.Errorf("invalid input sent %d requests", requests)
			}
		})
	}
}

func TestListIssueAndPRCommentsWithAll(t *testing.T) {
	previous := fetchAll
	fetchAll = true
	t.Cleanup(func() { fetchAll = previous })

	for _, tc := range []struct {
		name string
		run  func(*cobra.Command, []string) error
	}{
		{"issue comment list", runIssueCommentList},
		{"pr comments", runPRComments},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			cmd := safetyCommand(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/repos/owner/repo/issues/1/comments" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				fmt.Fprint(w, `[{"id":3,"user":{}},{"id":7,"user":{}},{"id":11,"user":{}}]`)
			})
			if err := cmd.Flags().Set("format", "json"); err != nil {
				t.Fatal(err)
			}
			out, err := safetyOutput(t, func() error { return tc.run(cmd, []string{"owner/repo", "1"}) })
			if err != nil {
				t.Fatal(err)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("sent %d comment requests, want 1", got)
			}
			var comments []gitea.Comment
			if err := json.Unmarshal([]byte(out), &comments); err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, 0, len(comments))
			for _, comment := range comments {
				ids = append(ids, comment.ID)
			}
			want := []int64{3, 7, 11}
			if !slices.Equal(ids, want) {
				t.Errorf("got %d comments, want exactly IDs %v in order", len(ids), want)
			}
		})
	}
}
