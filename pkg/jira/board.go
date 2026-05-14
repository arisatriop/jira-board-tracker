package jira

import (
	"context"
	"fmt"
	"sync"
)

type Board struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Self string `json:"self"`
}

type boardsResponse struct {
	Values     []Board `json:"values"`
	StartAt    int     `json:"startAt"`
	MaxResults int     `json:"maxResults"`
	Total      int     `json:"total"`
	IsLast     bool    `json:"isLast"`
}

type Sprint struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	State     string `json:"state"`
	Goal      string `json:"goal"`
	StartDate string `json:"startDate,omitempty"`
	EndDate   string `json:"endDate,omitempty"`
}

type sprintsResponse struct {
	Values []Sprint `json:"values"`
}

type Issue struct {
	ID     string      `json:"id"`
	Key    string      `json:"key"`
	Fields IssueFields `json:"fields"`
}

type SubtaskRef struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Summary   string      `json:"summary"`
		Status    IssueStatus `json:"status"`
		Assignee  *Assignee   `json:"assignee"`
		Priority  *Priority   `json:"priority"`
		IssueType IssueType   `json:"issuetype"`
	} `json:"fields"`
}

type IssueFields struct {
	Summary     string       `json:"summary"`
	Status      IssueStatus  `json:"status"`
	Assignee    *Assignee    `json:"assignee"`
	Priority    *Priority    `json:"priority"`
	IssueType   IssueType    `json:"issuetype"`
	Subtasks    []SubtaskRef `json:"subtasks"`
	StoryPoints *float64     `json:"customfield_10032"`
}

type IssueDetail struct {
	Key            string `json:"key"`
	Summary        string `json:"summary"`
	Description    string `json:"description"`
	Type           string `json:"type"`
	Status         string `json:"status"`
	Assignee       string `json:"assignee"`
	Priority       string `json:"priority"`
	StoryPoints    string `json:"story_points"`
	GithubRepo     string `json:"github_repo"`
	GithubBase     string `json:"github_base"`
	GithubFeature  string `json:"github_feature"`
}

type IssueStatus struct {
	Name           string         `json:"name"`
	StatusCategory StatusCategory `json:"statusCategory"`
}

type StatusCategory struct {
	Key string `json:"key"`
}

type Assignee struct {
	DisplayName string            `json:"displayName"`
	AvatarURLs  map[string]string `json:"avatarUrls"`
}

type Priority struct {
	Name string `json:"name"`
}

type IssueType struct {
	Name    string `json:"name"`
	IconURL string `json:"iconUrl"`
}

type issuesResponse struct {
	Issues []Issue `json:"issues"`
	Total  int     `json:"total"`
}

type SprintStats struct {
	Total int
	Done  int
}

type BoardStoryPointStats struct {
	TotalSP float64 `json:"total_sp"`
	StorySP float64 `json:"story_sp"`
}

type AssigneeStat struct {
	DisplayName string  `json:"display_name"`
	AvatarURL   string  `json:"avatar_url"`
	Count       int     `json:"count"`
	StoryPoints float64 `json:"story_points"`
}

type BoardSummary struct {
	Board         Board          `json:"board"`
	ActiveSprint  *Sprint        `json:"active_sprint,omitempty"`
	SprintStats   SprintStats    `json:"sprint_stats"`
	Issues        []Issue        `json:"issues"`
	TotalIssues   int            `json:"total_issues"`
	StatusStats   map[string]int `json:"status_stats"`
	TypeStats     map[string]int `json:"type_stats"`
	AssigneeStats []AssigneeStat `json:"assignee_stats"`
}

func (c *Client) GetBoards(ctx context.Context) ([]Board, error) {
	var all []Board
	startAt := 0
	for {
		var result boardsResponse
		path := fmt.Sprintf("/rest/agile/1.0/board?maxResults=50&startAt=%d", startAt)
		if err := c.do(ctx, "GET", path, &result); err != nil {
			return nil, err
		}
		all = append(all, result.Values...)
		if result.IsLast || len(result.Values) == 0 {
			break
		}
		startAt += len(result.Values)
	}
	return all, nil
}

func (c *Client) GetSprintStats(ctx context.Context, sprintID int) (SprintStats, error) {
	type countResp struct {
		Total int `json:"total"`
	}

	var total, done countResp
	var totalErr, doneErr error
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		totalErr = c.do(ctx, "GET", fmt.Sprintf("/rest/agile/1.0/sprint/%d/issue?maxResults=0", sprintID), &total)
	}()
	go func() {
		defer wg.Done()
		doneErr = c.do(ctx, "GET", fmt.Sprintf("/rest/agile/1.0/sprint/%d/issue?maxResults=0&jql=statusCategory%%3DDone", sprintID), &done)
	}()
	wg.Wait()

	if totalErr != nil {
		return SprintStats{}, totalErr
	}
	_ = doneErr
	return SprintStats{Total: total.Total, Done: done.Total}, nil
}

func (c *Client) GetLastFutureSprint(ctx context.Context, boardID int) (*Sprint, error) {
	var sprints sprintsResponse
	if err := c.do(ctx, "GET", fmt.Sprintf("/rest/agile/1.0/board/%d/sprint?state=future", boardID), &sprints); err != nil {
		return nil, err
	}
	if len(sprints.Values) == 0 {
		return nil, nil
	}
	last := &sprints.Values[0]
	for i := 1; i < len(sprints.Values); i++ {
		if sprints.Values[i].ID > last.ID {
			last = &sprints.Values[i]
		}
	}
	return last, nil
}

func (c *Client) GetActiveSprint(ctx context.Context, boardID int) (*Sprint, error) {
	var sprints sprintsResponse
	if err := c.do(ctx, "GET", fmt.Sprintf("/rest/agile/1.0/board/%d/sprint?state=active", boardID), &sprints); err != nil {
		return nil, err
	}
	if len(sprints.Values) == 0 {
		return nil, nil
	}
	return &sprints.Values[0], nil
}

func (c *Client) GetBoardStoryPoints(ctx context.Context, boardID int) (BoardStoryPointStats, error) {
	var stats BoardStoryPointStats

	// Resolve issue scope: active sprint → last future sprint → board fallback
	var baseURL string
	var board Board
	if err := c.do(ctx, "GET", fmt.Sprintf("/rest/agile/1.0/board/%d", boardID), &board); err != nil {
		return stats, fmt.Errorf("fetching board: %w", err)
	}
	if board.Type == "scrum" {
		var sprints sprintsResponse
		if err := c.do(ctx, "GET", fmt.Sprintf("/rest/agile/1.0/board/%d/sprint?state=active", boardID), &sprints); err == nil && len(sprints.Values) > 0 {
			baseURL = fmt.Sprintf("/rest/agile/1.0/sprint/%d/issue", sprints.Values[0].ID)
		} else if future, err := c.GetLastFutureSprint(ctx, boardID); err == nil && future != nil {
			baseURL = fmt.Sprintf("/rest/agile/1.0/sprint/%d/issue", future.ID)
		}
	}
	if baseURL == "" {
		baseURL = fmt.Sprintf("/rest/agile/1.0/board/%d/issue", boardID)
	}

	startAt := 0
	const perPage = 1000
	for {
		var page issuesResponse
		path := fmt.Sprintf("%s?maxResults=%d&startAt=%d&fields=customfield_10032,issuetype", baseURL, perPage, startAt)
		if err := c.do(ctx, "GET", path, &page); err != nil {
			return stats, err
		}
		for _, issue := range page.Issues {
			if issue.Fields.StoryPoints != nil {
				stats.TotalSP += *issue.Fields.StoryPoints
				if issue.Fields.IssueType.Name == "Story" {
					stats.StorySP += *issue.Fields.StoryPoints
				}
			}
		}
		if len(page.Issues) == 0 || startAt+len(page.Issues) >= page.Total {
			break
		}
		startAt += len(page.Issues)
	}
	return stats, nil
}

func (c *Client) GetBoardSummary(ctx context.Context, boardID int) (*BoardSummary, error) {
	var board Board
	if err := c.do(ctx, "GET", fmt.Sprintf("/rest/agile/1.0/board/%d", boardID), &board); err != nil {
		return nil, fmt.Errorf("fetching board: %w", err)
	}

	summary := &BoardSummary{Board: board}

	// Fetch active sprint first; fall back to last future sprint if none is running
	if board.Type == "scrum" {
		var sprints sprintsResponse
		if err := c.do(ctx, "GET", fmt.Sprintf("/rest/agile/1.0/board/%d/sprint?state=active", boardID), &sprints); err == nil && len(sprints.Values) > 0 {
			summary.ActiveSprint = &sprints.Values[0]
		} else {
			summary.ActiveSprint, _ = c.GetLastFutureSprint(ctx, boardID)
		}
	}

	var wg sync.WaitGroup

	// Issues — scoped to active sprint when available, otherwise fall back to board
	wg.Add(1)
	go func() {
		defer wg.Done()
		var baseURL string
		if summary.ActiveSprint != nil {
			baseURL = fmt.Sprintf("/rest/agile/1.0/sprint/%d/issue", summary.ActiveSprint.ID)
		} else {
			baseURL = fmt.Sprintf("/rest/agile/1.0/board/%d/issue", boardID)
		}

		// 1000 is the Jira API hard cap per request; most sprints fit in one call.
		// If total exceeds 1000, we paginate — each loop iteration is one API hit.
		var allIssues []Issue
		startAt := 0
		const perPage = 1000
		for {
			var page issuesResponse
			if err := c.do(ctx, "GET", fmt.Sprintf("%s?maxResults=%d&startAt=%d", baseURL, perPage, startAt), &page); err != nil {
				return
			}
			allIssues = append(allIssues, page.Issues...)
			summary.TotalIssues = page.Total
			if len(allIssues) >= page.Total || len(page.Issues) == 0 {
				break
			}
			startAt += len(page.Issues)
		}
		summary.Issues = allIssues

		summary.StatusStats = make(map[string]int)
		summary.TypeStats = make(map[string]int)
		assigneeMap := make(map[string]*AssigneeStat)

		for _, issue := range allIssues {
			summary.StatusStats[issue.Fields.Status.Name]++
			if issue.Fields.IssueType.Name != "" {
				summary.TypeStats[issue.Fields.IssueType.Name]++
			}
			if issue.Fields.Assignee != nil {
				name := issue.Fields.Assignee.DisplayName
				if _, ok := assigneeMap[name]; !ok {
					avatar := ""
					if url, exists := issue.Fields.Assignee.AvatarURLs["32x32"]; exists {
						avatar = url
					}
					assigneeMap[name] = &AssigneeStat{DisplayName: name, AvatarURL: avatar}
				}
				assigneeMap[name].Count++
				if issue.Fields.StoryPoints != nil {
					assigneeMap[name].StoryPoints += *issue.Fields.StoryPoints
				}
			}
		}
		for _, a := range assigneeMap {
			summary.AssigneeStats = append(summary.AssigneeStats, *a)
		}
	}()

	// Sprint stats — run concurrently with issues fetch
	if summary.ActiveSprint != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if stats, err := c.GetSprintStats(ctx, summary.ActiveSprint.ID); err == nil {
				summary.SprintStats = stats
			}
		}()
	}

	wg.Wait()

	return summary, nil
}
