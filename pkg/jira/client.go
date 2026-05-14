package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/arisatriop/jira-board-tracker/pkg/httpclient"
)

var (
	reWikiBold   = regexp.MustCompile(`\*([^*\n]+)\*`)
	reWikiItalic = regexp.MustCompile(`_([^_\n]+)_`)
	reWikiMono   = regexp.MustCompile(`\{\{([^}]+)\}\}`)
	reWikiLink   = regexp.MustCompile(`\[([^|\]]+)\|([^\]]+)\]`)
)

// wikiMarkupToHTML converts Jira wiki markup syntax to HTML.
func wikiMarkupToHTML(s string) string {
	if s == "" {
		return ""
	}
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
	lines := strings.Split(normalized, "\n")
	var sb strings.Builder
	inUL, inOL, inUL2 := false, false, false

	closeNested := func() {
		if inUL2 {
			sb.WriteString("</ul>")
			inUL2 = false
		}
	}
	closeList := func() {
		if inUL2 {
			sb.WriteString("</ul>")
			inUL2 = false
		}
		if inUL {
			sb.WriteString("</ul>")
			inUL = false
		}
		if inOL {
			sb.WriteString("</ol>")
			inOL = false
		}
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		// Nested unordered list (**) — must check before single *
		if strings.HasPrefix(line, "** ") {
			if inOL {
				sb.WriteString("</ol>")
				inOL = false
			}
			if !inUL {
				sb.WriteString(`<ul style="list-style-type:disc;padding-left:1.5em;margin:0.3em 0">`)
				inUL = true
			}
			if !inUL2 {
				sb.WriteString(`<ul style="list-style-type:circle;padding-left:1.25em;margin:0.1em 0">`)
				inUL2 = true
			}
			sb.WriteString(`<li style="margin:0.1em 0">` + inlineWiki(strings.TrimPrefix(line, "** ")) + "</li>")
			continue
		}
		// Unordered list (*)
		if strings.HasPrefix(line, "* ") {
			closeNested()
			if inOL {
				sb.WriteString("</ol>")
				inOL = false
			}
			if !inUL {
				sb.WriteString(`<ul style="list-style-type:disc;padding-left:1.5em;margin:0.3em 0">`)
				inUL = true
			}
			sb.WriteString(`<li style="margin:0.15em 0">` + inlineWiki(strings.TrimPrefix(line, "* ")) + "</li>")
			continue
		}
		// Ordered list (#)
		if strings.HasPrefix(line, "# ") {
			closeNested()
			if inUL {
				sb.WriteString("</ul>")
				inUL = false
			}
			if !inOL {
				sb.WriteString(`<ol style="list-style-type:decimal;padding-left:1.5em;margin:0.3em 0">`)
				inOL = true
			}
			sb.WriteString(`<li style="margin:0.15em 0">` + inlineWiki(strings.TrimPrefix(line, "# ")) + "</li>")
			continue
		}
		closeList()

		headingDone := false
		for lvl := 1; lvl <= 6; lvl++ {
			prefix := fmt.Sprintf("h%d. ", lvl)
			if strings.HasPrefix(line, prefix) {
				raw := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				if raw == "" && i+1 < len(lines) {
					i++
					raw = strings.TrimSpace(lines[i])
				}
				content := inlineWiki(raw)
				fontSize := []string{"1.4em", "1.25em", "1.1em", "1em", "0.95em", "0.9em"}[lvl-1]
				sb.WriteString(fmt.Sprintf(`<h%d style="font-weight:bold;font-size:%s;margin-top:0.75em;margin-bottom:0.25em">%s</h%d>`, lvl, fontSize, content, lvl))
				headingDone = true
				break
			}
		}
		if headingDone {
			continue
		}

		if line == "----" {
			sb.WriteString("<hr>")
			continue
		}
		if strings.TrimSpace(line) == "" {
			sb.WriteString("<br>")
			continue
		}
		sb.WriteString("<p>" + inlineWiki(line) + "</p>")
	}
	closeList()
	return sb.String()
}

// inlineWiki applies inline Jira wiki formatting (bold, italic, code, links) to HTML-escaped text.
func inlineWiki(s string) string {
	s = html.EscapeString(s)
	s = reWikiLink.ReplaceAllString(s, `<a href="$2" target="_blank" rel="noopener">$1</a>`)
	s = reWikiBold.ReplaceAllString(s, `<strong style="font-weight:bold">$1</strong>`)
	s = reWikiItalic.ReplaceAllString(s, `<em>$1</em>`)
	s = reWikiMono.ReplaceAllString(s, `<code>$1</code>`)
	return s
}

type Client struct {
	baseURL    string
	email      string
	apiToken   string
	httpClient *http.Client
}

func NewClient(baseURL, email, apiToken string) *Client {
	return &Client{
		baseURL:    baseURL,
		email:      email,
		apiToken:   apiToken,
		httpClient: httpclient.NewClient(10 * time.Second),
	}
}

func (c *Client) GetIssue(ctx context.Context, issueKey, githubRepoField, githubBaseField, githubFeatureField string) (*IssueDetail, error) {
	fields := "summary,description,status,assignee,priority,issuetype,customfield_10032"
	if githubRepoField != "" {
		fields += "," + githubRepoField
	}
	if githubBaseField != "" {
		fields += "," + githubBaseField
	}
	if githubFeatureField != "" {
		fields += "," + githubFeatureField
	}

	var raw struct {
		Key    string                     `json:"key"`
		Fields map[string]json.RawMessage `json:"fields"`
	}
	path := fmt.Sprintf("/rest/api/2/issue/%s?fields=%s", issueKey, fields)
	if err := c.do(ctx, "GET", path, &raw); err != nil {
		return nil, err
	}

	detail := &IssueDetail{Key: raw.Key}
	f := raw.Fields

	detail.Summary = rawString(f["summary"])
	detail.Description = rawDescription(f["description"])

	var status IssueStatus
	if b, ok := f["status"]; ok {
		json.Unmarshal(b, &status) //nolint:errcheck
		detail.Status = status.Name
	}

	var assignee Assignee
	if b, ok := f["assignee"]; ok {
		json.Unmarshal(b, &assignee) //nolint:errcheck
		detail.Assignee = assignee.DisplayName
	}

	var priority Priority
	if b, ok := f["priority"]; ok {
		json.Unmarshal(b, &priority) //nolint:errcheck
		detail.Priority = priority.Name
	}

	var issueType IssueType
	if b, ok := f["issuetype"]; ok {
		json.Unmarshal(b, &issueType) //nolint:errcheck
		detail.Type = issueType.Name
	}

	if b, ok := f["customfield_10032"]; ok {
		var sp float64
		if json.Unmarshal(b, &sp) == nil && sp > 0 {
			if sp == float64(int64(sp)) {
				detail.StoryPoints = strconv.Itoa(int(sp))
			} else {
				detail.StoryPoints = strconv.FormatFloat(sp, 'f', 1, 64)
			}
		}
	}

	if githubRepoField != "" {
		detail.GithubRepo = rawString(f[githubRepoField])
	}
	if githubBaseField != "" {
		detail.GithubBase = rawString(f[githubBaseField])
	}
	if githubFeatureField != "" {
		detail.GithubFeature = rawString(f[githubFeatureField])
	}

	return detail, nil
}

// rawString extracts a plain string from a JSON raw message.
func rawString(b json.RawMessage) string {
	if b == nil {
		return ""
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		return s
	}
	return ""
}

// rawDescription extracts readable text from a Jira description field,
// which may be a plain string (REST v2 classic) or an ADF object (REST v3 / next-gen).
func rawDescription(b json.RawMessage) string {
	if b == nil {
		return ""
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		return wikiMarkupToHTML(s)
	}
	var doc map[string]interface{}
	if json.Unmarshal(b, &doc) == nil {
		return strings.TrimSpace(adfText(doc))
	}
	return ""
}

func adfText(node map[string]interface{}) string {
	var sb strings.Builder
	if t, _ := node["type"].(string); t == "text" {
		if text, ok := node["text"].(string); ok {
			sb.WriteString(text)
		}
	}
	if marks, ok := node["content"].([]interface{}); ok {
		for _, child := range marks {
			if m, ok := child.(map[string]interface{}); ok {
				sb.WriteString(adfText(m))
			}
		}
	}
	switch t, _ := node["type"].(string); t {
	case "paragraph", "heading", "bulletList", "orderedList", "listItem", "codeBlock", "hardBreak":
		sb.WriteString("\n")
	}
	return sb.String()
}

func (c *Client) do(ctx context.Context, method, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.SetBasicAuth(c.email, c.apiToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("jira returned status %d", resp.StatusCode)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
