package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jbolus-owens/factory/internal/protocol"
)

const reviewCommentMarkerPrefix = "<!-- factory-review:"

type gitHubIssueComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

func reviewCommentMarker(attemptID string) string {
	return reviewCommentMarkerPrefix + attemptID + " -->"
}

func workCommentBody(config Config, attemptID, stage string, reports []reviewResult, repaired bool) string {
	var body strings.Builder
	body.WriteString(reviewCommentMarker(attemptID))
	body.WriteString("\n## Factory work\n\n")
	fmt.Fprintf(&body, "Worker: `%s`\n\n", config.Name)
	for _, role := range []string{plannerRole, executorRole, reviewerRole} {
		fmt.Fprintf(&body, "%s: %s\n\n", strings.ToUpper(role[:1])+role[1:], roleCommentProfile(config, role))
	}
	body.WriteString(stage)
	body.WriteString("\n")
	if len(reports) == 0 {
		return boundedText(body.String(), 60<<10)
	}
	body.WriteString("\n## Factory independent review\n\n")
	for index, report := range reports {
		fmt.Fprintf(&body, "### Pass %d: %s\n\n", index+1, strings.ToUpper(report.Verdict))
		fmt.Fprintf(&body, "Effective reviewer: %s\n\n", reportCommentProfile(config, report))
		if len(report.Findings) == 0 {
			body.WriteString("No findings.\n\n")
			continue
		}
		for _, finding := range report.Findings {
			fmt.Fprintf(&body, "- **%s**", finding.Title)
			if finding.Location != "" {
				fmt.Fprintf(&body, " (`%s`)", finding.Location)
			}
			fmt.Fprintf(&body, ": %s\n", finding.Detail)
		}
		body.WriteString("\n")
	}
	if repaired {
		body.WriteString("Repair: attempted at `high` effort.\n\n")
	}
	if reports[len(reports)-1].Verdict == reviewApproved {
		body.WriteString("Final verdict: **APPROVED**\n")
	} else {
		body.WriteString("Final verdict: **FAILED**\n")
	}
	return boundedText(body.String(), 60<<10)
}

func roleCommentProfile(config Config, role string) string {
	profile, err := config.profile(role)
	if err != nil {
		return "not configured"
	}
	return profileComment(profile)
}

func reportCommentProfile(config Config, report reviewResult) string {
	if report.profile.Name != "" {
		return profileComment(report.profile)
	}
	return roleCommentProfile(config, reviewerRole)
}

func profileComment(profile ProfileConfig) string {
	return fmt.Sprintf("`%s` (`%s` / `%s` / `%s` / `%s`)", profile.Name, profile.Adapter,
		profile.Provider, profile.Model, profile.ReasoningEffort)
}

func (manager *Manager) commentReview(ctx context.Context, claim protocol.Claim, value worktree, reports []reviewResult, repaired bool) error {
	if _, err := manager.config.profile(reviewerRole); err != nil {
		return err
	}
	body := workCommentBody(manager.config, claim.Attempt.ID, reviewStage(manager.config, "completed."), reports, repaired)
	return manager.commentIssue(ctx, claim, value, body)
}

func (manager *Manager) commentReviewFailure(ctx context.Context, claim protocol.Claim, value worktree, reports []reviewResult, repaired bool, reviewErr error) error {
	stage := reviewStage(manager.config, "failed.\n\nError: "+reviewErr.Error())
	body := workCommentBody(manager.config, claim.Attempt.ID, stage, reports, repaired)
	return manager.commentIssue(ctx, claim, value, body)
}

func planningStage(config Config) string {
	if config.Roles.Planner == "" {
		return "Planning: skipped."
	}
	return "Planning: completed."
}

func reviewStage(config Config, review string) string {
	return planningStage(config) + "\n\nImplementation: completed.\n\nReview: " + review
}

func reviewProgressStage(planning string) string {
	return planning + "\n\nImplementation: completed.\n\nReview: running."
}

func executionStage(planning, state, failure string) string {
	implementation := "completed"
	verdict := "SUCCEEDED"
	if state == "cancelled" {
		implementation = "cancelled"
		verdict = "CANCELLED"
	} else if state != "succeeded" {
		implementation = "failed"
		verdict = "FAILED"
	}
	stage := planning + "\n\nImplementation: " + implementation + ".\n\nReview: skipped.\n\nFinal verdict: **" + verdict + "**"
	if failure == "" {
		return stage
	}
	return stage + "\n\nError: " + failure
}

func (manager *Manager) commentProgress(ctx context.Context, claim protocol.Claim, value worktree, stage string) error {
	body := workCommentBody(manager.config, claim.Attempt.ID, stage, nil, false)
	return manager.commentIssue(ctx, claim, value, body)
}

func (manager *Manager) commentIssue(ctx context.Context, claim protocol.Claim, value worktree, body string) error {
	if claim.Session.Target.SourceKind != "github_issue" {
		return nil
	}
	repository := strings.TrimPrefix(strings.ToLower(claim.Repository.RemoteIdentity), "github.com/")
	issue, err := issueNumber(claim.Session.Target.SourceReference)
	if err != nil {
		return err
	}
	stdout, stderr, err := runCommand(ctx, manager.options.GitHubExecutable, value.Path, 64<<10, "api", "user")
	if err != nil {
		return commandFailure("read GitHub identity", stdout, stderr, err)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(stdout, &user); err != nil || user.Login == "" {
		return fmt.Errorf("read GitHub identity: invalid response")
	}
	path := "repos/" + repository + "/issues/" + strconv.FormatInt(issue, 10) + "/comments"
	stdout, stderr, err = runCommand(ctx, manager.options.GitHubExecutable, value.Path, 256<<10, "api", path, "--paginate", "--slurp")
	if err != nil {
		return commandFailure("list GitHub issue comments", stdout, stderr, err)
	}
	var pages [][]gitHubIssueComment
	if err := json.Unmarshal(stdout, &pages); err != nil {
		return fmt.Errorf("decode GitHub issue comments: %w", err)
	}
	for _, comments := range pages {
		for _, comment := range comments {
			if comment.User.Login != user.Login || !strings.Contains(comment.Body, reviewCommentMarker(claim.Attempt.ID)) {
				continue
			}
			stdout, stderr, err = runCommand(ctx, manager.options.GitHubExecutable, value.Path, 64<<10,
				"api", "--method", "PATCH", "repos/"+repository+"/issues/comments/"+strconv.FormatInt(comment.ID, 10), "-f", "body="+body)
			if err != nil {
				return commandFailure("update GitHub issue review", stdout, stderr, err)
			}
			return nil
		}
	}
	stdout, stderr, err = runCommand(ctx, manager.options.GitHubExecutable, value.Path, 64<<10,
		"api", "--method", "POST", path, "-f", "body="+body)
	if err != nil {
		return commandFailure("create GitHub issue review", stdout, stderr, err)
	}
	return nil
}

func issueNumber(reference string) (int64, error) {
	parts := strings.Split(strings.TrimSuffix(reference, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] != "issues" {
		return 0, fmt.Errorf("invalid GitHub issue reference %q", reference)
	}
	value, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("invalid GitHub issue reference %q", reference)
	}
	return value, nil
}
