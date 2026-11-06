package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"code.gitea.io/sdk/gitea"
	"github.com/rayfish/teacli/modules/errors"
	"github.com/spf13/cobra"
)

type actionRunJobsResponse struct {
	Jobs       []*gitea.ActionWorkflowJob `json:"jobs"`
	TotalCount *int64                     `json:"total_count"`
	Array      bool                       `json:"-"`
}

func (r *actionRunJobsResponse) UnmarshalJSON(data []byte) error {
	type object actionRunJobsResponse
	var obj object
	if err := json.Unmarshal(data, &obj); err == nil {
		*r = actionRunJobsResponse(obj)
		return nil
	}
	var jobs []*gitea.ActionWorkflowJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		return err
	}
	r.Jobs = jobs
	r.TotalCount = nil
	r.Array = true
	return nil
}

func fetchActionRunJobs(cmd *cobra.Command, owner, repo string, id int64, opts gitea.ListOptions, all bool) ([]*gitea.ActionWorkflowJob, bool, error) {
	base := fmt.Sprintf("repos/%s/%s/actions/runs/%d/jobs", url.PathEscape(owner), url.PathEscape(repo), id)
	if all {
		opts.Page = 1
		if opts.PageSize <= 0 {
			opts.PageSize = defaultPageSize
		}
	}
	jobs := make([]*gitea.ActionWorkflowJob, 0)
	for page := 0; page < maxPages; page++ {
		query := url.Values{}
		if opts.Page > 0 {
			query.Set("page", strconv.Itoa(opts.Page))
		}
		if opts.PageSize > 0 {
			query.Set("limit", strconv.Itoa(opts.PageSize))
		}
		path := base
		if len(query) > 0 {
			path += "?" + query.Encode()
		}
		data, err := apiRequest(cmd, http.MethodGet, path, nil)
		if err != nil {
			return nil, false, err
		}
		var result actionRunJobsResponse
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, false, errors.NewGeneralError(fmt.Sprintf("failed to decode response: %v", err))
		}
		jobs = append(jobs, result.Jobs...)
		if result.Array {
			return jobs, true, nil
		}
		if !all || len(result.Jobs) == 0 || (result.TotalCount != nil && int64(len(jobs)) >= *result.TotalCount) {
			return jobs, false, nil
		}
		opts.Page++
	}
	return nil, false, errors.NewGeneralError("too many pages of workflow jobs")
}
