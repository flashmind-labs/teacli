// Package cmd contains CI/CD actions; this file blocks until a run finishes.
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"code.gitea.io/sdk/gitea"
	"github.com/rayfish/teacli/modules/errors"
	"github.com/rayfish/teacli/modules/output"

	"github.com/spf13/cobra"
)

// inFlightStatuses are the run statuses that mean the run has not settled yet.
// Gitea reports both its internal names (waiting, running, blocked) and the
// GitHub-compatible ones (queued, in_progress) depending on the endpoint, so
// both sets are listed and anything unknown counts as terminal.
var inFlightStatuses = map[string]bool{
	"pending":     true,
	"queued":      true,
	"waiting":     true,
	"running":     true,
	"in_progress": true,
	"blocked":     true,
}

// runInFlight reports whether a run with this status is still going.
func runInFlight(status string) bool {
	return inFlightStatuses[status]
}

// runOutcome is how the run ended. Gitea fills conclusion on completed runs but
// leaves it empty on some, putting the result in status instead.
func runOutcome(run *gitea.ActionWorkflowRun) string {
	if run.Conclusion != "" {
		return run.Conclusion
	}
	return run.Status
}

// watchOutcomeError turns a non-success outcome into an exit-1 error, so
// `teacli action watch 42 && deploy` does the right thing.
func watchOutcomeError(run *gitea.ActionWorkflowRun, input string) error {
	outcome := runOutcome(run)
	if outcome == "success" {
		return nil
	}
	return errors.NewGeneralError(fmt.Sprintf("workflow run %s finished: %s", input, outcome))
}

// validateWatchTiming rejects timings that would never poll or never stop. A
// zero timeout is allowed and means "no limit".
func validateWatchTiming(interval, timeout time.Duration) error {
	if interval <= 0 {
		return errors.NewValidationError("--interval must be greater than zero",
			map[string]interface{}{"value": interval.String()})
	}
	if timeout < 0 {
		return errors.NewValidationError("--timeout cannot be negative",
			map[string]interface{}{"value": timeout.String()})
	}
	return nil
}

// runWatcher polls a single workflow run until it reaches a terminal state. The
// clock and the fetch are injected so tests can drive long waits instantly.
type runWatcher struct {
	fetch    func() (*gitea.ActionWorkflowRun, error)
	interval time.Duration
	timeout  time.Duration
	now      func() time.Time
	sleep    func(time.Duration)
	onChange func(status string)
	onRun    func(*gitea.ActionWorkflowRun) error
}

// wait returns the run once it stops running, or an error if it never does
// before the timeout.
func (w runWatcher) wait() (*gitea.ActionWorkflowRun, error) {
	deadline := w.now().Add(w.timeout)
	previous := ""
	failures := 0

	for {
		run, err := w.fetch()
		wait := w.interval
		if err != nil {
			failures++
			if failures >= 3 {
				return nil, &errors.CLIError{Code: 6, Message: fmt.Sprintf("API error after 3 attempts: %v", err)}
			}
			wait *= time.Duration(1 << (failures - 1))
		} else {
			failures = 0
			if w.onRun != nil {
				if err := w.onRun(run); err != nil {
					return nil, err
				}
			}

			if run.Status != previous {
				previous = run.Status
				if w.onChange != nil {
					w.onChange(run.Status)
				}
			}

			if !runInFlight(run.Status) {
				return run, nil
			}

		}
		if w.timeout > 0 {
			left := deadline.Sub(w.now())
			if left <= 0 {
				return nil, &errors.CLIError{Code: 5, Message: fmt.Sprintf(
					"timed out after %s waiting for the workflow run to finish", w.timeout)}
			}
			// Trim the last wait so the run is polled once at the deadline
			// rather than skipped past it.
			if left < wait {
				wait = left
			}
		}

		w.sleep(wait)
	}
}

var actionWatchCmd = &cobra.Command{
	Use:     "watch [<owner>/<repo>] <run-id>",
	Aliases: []string{"wait"},
	Short:   "Wait for a workflow run to finish",
	Long: `Poll a workflow run until it finishes.

Exits 0 when the run concludes successfully and 1 for any other conclusion, so
it composes: teacli action watch 42 && teacli release create v1.0.0`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runActionWatch,
}

func init() {
	actionCmd.AddCommand(actionWatchCmd)

	actionWatchCmd.Flags().Duration("interval", 5*time.Second, "Time between polls")
	actionWatchCmd.Flags().Duration("timeout", 30*time.Minute, "Give up after this long (0 waits forever)")
}

func runActionWatch(cmd *cobra.Command, args []string) error {
	owner, repo, args, err := repoTarget(cmd, args, 1)
	if err != nil {
		return err
	}

	interval, _ := cmd.Flags().GetDuration("interval")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	if err := validateWatchTiming(interval, timeout); err != nil {
		return err
	}

	client, err := getClient(cmd)
	if err != nil {
		return err
	}

	printer := getPrinter(cmd)

	if dryRunf(cmd, "Would watch workflow run %s in %s/%s (every %s, up to %s)",
		args[1], owner, repo, interval, timeout) {
		return nil
	}

	started := time.Now()
	var runID int64
	for attempt := 0; attempt < 3; attempt++ {
		runID, err = resolveActionRunID(cmd, client, owner, repo, args[1])
		if err == nil {
			break
		}
		if errors.ExitCode(err) != errors.ExitGeneralError {
			return err
		}
		if attempt == 2 {
			return &errors.CLIError{Code: 6, Message: fmt.Sprintf("API error after 3 attempts: %v", err)}
		}
		delay := interval * time.Duration(1<<attempt)
		if timeout > 0 {
			left := timeout - time.Since(started)
			if left <= 0 {
				return &errors.CLIError{Code: 5, Message: fmt.Sprintf("timed out after %s waiting for the workflow run to finish", timeout)}
			}
			if delay > left {
				delay = left
			}
		}
		time.Sleep(delay)
	}

	remaining := timeout
	if timeout > 0 {
		remaining = timeout - time.Since(started)
		if remaining <= 0 {
			return &errors.CLIError{Code: 5, Message: fmt.Sprintf("timed out after %s waiting for the workflow run to finish", timeout)}
		}
	}
	var snapshot watchSnapshot
	var previous []byte
	watcher := runWatcher{
		fetch: func() (*gitea.ActionWorkflowRun, error) {
			if printer.Format() == output.FormatJSON {
				var err error
				snapshot, err = fetchWatchSnapshot(cmd, owner, repo, runID)
				if err != nil {
					return nil, err
				}
				return &snapshot.run.ActionWorkflowRun, nil
			}
			run, resp, err := client.GetRepoActionRun(owner, repo, runID)
			if err != nil {
				return nil, errors.FromGiteaNotFound(resp, err, "workflow run", args[1])
			}
			return run, nil
		},
		interval: interval,
		timeout:  remaining,
		now:      time.Now,
		sleep:    time.Sleep,
	}

	if printer.Format() == output.FormatJSON {
		watcher.onRun = func(run *gitea.ActionWorkflowRun) error {
			state, err := json.Marshal(snapshot)
			if err != nil {
				return err
			}
			if !bytes.Equal(previous, state) {
				line, err := json.Marshal(snapshot.output())
				if err != nil {
					return err
				}
				if _, err = fmt.Fprintln(cmd.OutOrStdout(), string(line)); err != nil {
					return err
				}
				previous = state
			}
			return nil
		}
	}

	// Progress goes to stderr, and only on a status change, so stdout stays a
	// single parseable document and a long wait does not fill a log.
	if printer.Format() != output.FormatJSON {
		watcher.onChange = func(status string) {
			fmt.Fprintf(cmd.ErrOrStderr(), "run %s: %s\n", args[1], status)
		}
	}

	finished, err := watcher.wait()
	if err != nil {
		return err
	}

	if printer.Format() == output.FormatJSON {
		return watchOutcomeError(finished, args[1])
	}
	if err := printer.Emit(finished, func() error {
		return printActionRunText(printer, finished)
	}); err != nil {
		return err
	}

	return watchOutcomeError(finished, args[1])
}

// watchRun retains fields missing from the SDK's run type.
type watchRun struct {
	gitea.ActionWorkflowRun
	Name      string     `json:"name"`
	UpdatedAt *time.Time `json:"updated_at"`
}

func (r *watchRun) UnmarshalJSON(data []byte) error {
	type plain watchRun
	var fields struct {
		*plain
		Title       string      `json:"title"`
		WorkflowID  string      `json:"workflow_id"`
		Index       int64       `json:"index_in_repo"`
		PrettyRef   string      `json:"prettyref"`
		CommitSHA   string      `json:"commit_sha"`
		TriggerUser *gitea.User `json:"trigger_user"`
		Started     *time.Time  `json:"started"`
		Updated     *time.Time  `json:"updated"`
	}
	fields.plain = (*plain)(r)
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if r.RunNumber == 0 {
		r.RunNumber = fields.Index
	}
	if r.Name == "" {
		r.Name = fields.WorkflowID
		if r.Name == "" {
			r.Name = fields.Title
		}
	}
	if r.HeadBranch == "" {
		r.HeadBranch = strings.TrimPrefix(fields.PrettyRef, "refs/heads/")
	}
	if r.HeadSha == "" {
		r.HeadSha = fields.CommitSHA
	}
	if r.Actor == nil {
		r.Actor = fields.TriggerUser
	}
	if r.StartedAt.IsZero() && fields.Started != nil {
		r.StartedAt = *fields.Started
	}
	if r.UpdatedAt == nil {
		r.UpdatedAt = fields.Updated
	}
	return nil
}

type watchSnapshot struct {
	run  watchRun
	jobs []*gitea.ActionWorkflowJob
}

// MarshalJSON includes the full API state for change detection.
func (s watchSnapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Run  watchRun                   `json:"run"`
		Jobs []*gitea.ActionWorkflowJob `json:"jobs"`
	}{s.run, s.jobs})
}

type watchStep struct {
	Number int64  `json:"number"`
	Total  int    `json:"total"`
	Name   string `json:"name"`
}
type watchJob struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Conclusion  *string    `json:"conclusion"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	Step        *watchStep `json:"step"`
}
type watchOutput struct {
	RunID      int64      `json:"run_id"`
	Status     string     `json:"status"`
	Conclusion *string    `json:"conclusion"`
	Workflow   string     `json:"workflow"`
	Branch     string     `json:"branch"`
	Event      string     `json:"event"`
	Actor      string     `json:"actor"`
	URL        string     `json:"url"`
	StartedAt  *time.Time `json:"started_at"`
	UpdatedAt  *time.Time `json:"updated_at"`
	Jobs       []watchJob `json:"jobs"`
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
func (s watchSnapshot) output() watchOutput {
	r := s.run
	actor := ""
	if r.Actor != nil {
		actor = r.Actor.UserName
	}
	workflow := r.Name
	if workflow == "" {
		workflow = r.DisplayTitle
	}
	if workflow == "" {
		workflow = r.Path
	}
	link := r.HTMLURL
	if link == "" {
		link = r.URL
	}
	out := watchOutput{r.ID, r.Status, optionalString(r.Conclusion), workflow, r.HeadBranch, r.Event, actor, link, optionalTime(r.StartedAt), r.UpdatedAt, make([]watchJob, 0, len(s.jobs))}
	for _, j := range s.jobs {
		job := watchJob{j.ID, j.Name, j.Status, optionalString(j.Conclusion), optionalTime(j.StartedAt), optionalTime(j.CompletedAt), nil}
		for _, step := range j.Steps {
			if step != nil && (step.Status == "running" || step.Status == "in_progress") {
				job.Step = &watchStep{step.Number, len(j.Steps), step.Name}
				break
			}
		}
		out.Jobs = append(out.Jobs, job)
	}
	return out
}
func fetchWatchSnapshot(cmd *cobra.Command, owner, repo string, id int64) (watchSnapshot, error) {
	var s watchSnapshot
	var err error
	base := fmt.Sprintf("repos/%s/%s/actions/runs/%d", url.PathEscape(owner), url.PathEscape(repo), id)
	if err := apiGetInto(cmd, base, &s.run); err != nil {
		return s, err
	}
	var isArray bool
	s.jobs, isArray, err = fetchActionRunJobs(cmd, owner, repo, id, gitea.ListOptions{Page: 1, PageSize: 50}, true)
	if err != nil {
		if errors.ExitCode(err) == errors.ExitNotFound {
			s.jobs = nil
			return s, nil
		}
		return s, err
	}
	if isArray {
		for i, job := range s.jobs {
			path := fmt.Sprintf("repos/%s/%s/actions/jobs/%d", url.PathEscape(owner), url.PathEscape(repo), job.ID)
			var detail gitea.ActionWorkflowJob
			if err := apiGetInto(cmd, path, &detail); err != nil {
				if errors.ExitCode(err) == errors.ExitNotFound {
					s.jobs = nil
					return s, nil
				}
				return s, err
			}
			s.jobs[i] = &detail
		}
	}
	return s, nil
}
