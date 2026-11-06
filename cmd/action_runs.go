package cmd

import (
	"fmt"
	"net/url"
	"strconv"

	"code.gitea.io/sdk/gitea"
	"github.com/rayfish/teacli/modules/errors"
	"github.com/spf13/cobra"
)

func fetchActionRuns(cmd *cobra.Command, owner, repo string, opts gitea.ListRepoActionRunsOptions, all bool) ([]*gitea.ActionWorkflowRun, error) {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	wantedPage := opts.Page
	if wantedPage <= 0 {
		wantedPage = 1
	}
	scan := all || opts.Branch != ""
	page := wantedPage
	if scan {
		page = 1
	}
	runs := make([]*gitea.ActionWorkflowRun, 0)
	for n := 0; n < maxPages; n++ {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		q.Set("limit", strconv.Itoa(pageSize))
		if opts.Status != "" {
			q.Set("status", opts.Status)
		}
		if opts.Event != "" {
			q.Set("event", opts.Event)
		}
		if opts.Branch != "" {
			q.Set("branch", opts.Branch)
		}
		path := fmt.Sprintf("repos/%s/%s/actions/runs?%s", url.PathEscape(owner), url.PathEscape(repo), q.Encode())
		var result struct {
			Runs []watchRun `json:"workflow_runs"`
		}
		if err := apiGetInto(cmd, path, &result); err != nil {
			return nil, err
		}
		for i := range result.Runs {
			r := &result.Runs[i].ActionWorkflowRun
			if opts.Branch == "" || r.HeadBranch == opts.Branch {
				runs = append(runs, r)
			}
		}
		if !scan || len(result.Runs) == 0 {
			break
		}
		if !all && len(runs) >= wantedPage*pageSize {
			break
		}
		page++
		if n == maxPages-1 {
			return nil, errors.NewGeneralError("too many pages of workflow runs")
		}
	}
	if !all && opts.Branch != "" {
		start := (wantedPage - 1) * pageSize
		if start >= len(runs) {
			return runs[:0], nil
		}
		end := start + pageSize
		if end > len(runs) {
			end = len(runs)
		}
		runs = runs[start:end]
	}
	return runs, nil
}
