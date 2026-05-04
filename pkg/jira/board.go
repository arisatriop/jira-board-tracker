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

type IssueFields struct {
	Summary   string      `json:"summary"`
	Status    IssueStatus `json:"status"`
	Assignee  *Assignee   `json:"assignee"`
	Priority  *Priority   `json:"priority"`
	IssueType IssueType   `json:"issuetype"`
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

type AssigneeStat struct {
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
	Count       int    `json:"count"`
}

type BoardSummary struct {
	Board          Board           `json:"board"`
	ActiveSprint   *Sprint         `json:"active_sprint,omitempty"`
	SprintStats    SprintStats     `json:"sprint_stats"`
	Issues         []Issue         `json:"issues"`
	TotalIssues    int             `json:"total_issues"`
	StatusStats    map[string]int  `json:"status_stats"`
	TypeStats      map[string]int  `json:"type_stats"`
	AssigneeStats  []AssigneeStat  `json:"assignee_stats"`
}

func (c *Client) GetBoards(ctx context.Context) ([]Board, error) {
	var result boardsResponse
	if err := c.do(ctx, "GET", "/rest/agile/1.0/board", &result); err != nil {
		return nil, err
	}
	return result.Values, nil
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

func (c *Client) GetBoardSummary(ctx context.Context, boardID int) (*BoardSummary, error) {
	var board Board
	if err := c.do(ctx, "GET", fmt.Sprintf("/rest/agile/1.0/board/%d", boardID), &board); err != nil {
		return nil, fmt.Errorf("fetching board: %w", err)
	}

	summary := &BoardSummary{Board: board}

	// Fetch active sprint first so we can scope issues to it
	if board.Type == "scrum" {
		var sprints sprintsResponse
		if err := c.do(ctx, "GET", fmt.Sprintf("/rest/agile/1.0/board/%d/sprint?state=active", boardID), &sprints); err == nil && len(sprints.Values) > 0 {
			summary.ActiveSprint = &sprints.Values[0]
		}
	}

	var wg sync.WaitGroup

	// Issues — scoped to active sprint when available, otherwise fall back to board
	wg.Add(1)
	go func() {
		defer wg.Done()
		var issues issuesResponse
		var path string
		if summary.ActiveSprint != nil {
			path = fmt.Sprintf("/rest/agile/1.0/sprint/%d/issue?maxResults=200", summary.ActiveSprint.ID)
		} else {
			path = fmt.Sprintf("/rest/agile/1.0/board/%d/issue?maxResults=50", boardID)
		}
		if err := c.do(ctx, "GET", path, &issues); err != nil {
			return
		}
		summary.Issues = issues.Issues
		summary.TotalIssues = issues.Total
		summary.StatusStats = make(map[string]int)
		summary.TypeStats = make(map[string]int)
		assigneeMap := make(map[string]*AssigneeStat)

		for _, issue := range issues.Issues {
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
