package handler

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/arisatriop/jira-board-tracker/config"
	pkgjira "github.com/arisatriop/jira-board-tracker/pkg/jira"
	"github.com/arisatriop/jira-board-tracker/pkg/response"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	googleoauth2 "google.golang.org/api/oauth2/v2"
	"google.golang.org/api/option"
)

type Jira struct {
	client             *pkgjira.Client
	apiKey             string
	oauthCfg           *oauth2.Config
	sessionKey         []byte
	claudeRunnerURL    string
	githubRepoField    string
	githubBaseField    string
	githubFeatureField string
}

type jiraSessionClaims struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	jwt.RegisteredClaims
}

func NewJira(client *pkgjira.Client, apiKey string, googleCfg config.GoogleOAuth, claudeRunnerURL, githubRepoField, githubBaseField, githubFeatureField string) *Jira {
	h := &Jira{
		client:             client,
		apiKey:             apiKey,
		claudeRunnerURL:    claudeRunnerURL,
		githubRepoField:    githubRepoField,
		githubBaseField:    githubBaseField,
		githubFeatureField: githubFeatureField,
	}
	if googleCfg.ClientID != "" {
		h.oauthCfg = &oauth2.Config{
			ClientID:     googleCfg.ClientID,
			ClientSecret: googleCfg.ClientSecret,
			RedirectURL:  googleCfg.RedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		}
		key := sha256.Sum256([]byte(googleCfg.ClientSecret + apiKey))
		h.sessionKey = key[:]
	}
	return h
}

type claudeRunnerPrompt struct {
	Whats       []string `json:"whats"`
	Hows        []string `json:"hows"`
	Acceptances []string `json:"acceptances"`
}

type claudeRunnerReq struct {
	TicketID      string             `json:"ticketId"`
	RepoURL       string             `json:"repoUrl"`
	BaseBranch    string             `json:"baseBranch"`
	FeatureBranch string             `json:"featureBranch"`
	Prompt        claudeRunnerPrompt `json:"prompt"`
}

func (h *Jira) GetTicketDetail(ctx *fiber.Ctx) error {
	key := ctx.Params("key")
	if key == "" {
		return ctx.Status(fiber.StatusBadRequest).JSON(map[string]interface{}{
			"success": false,
			"message": "Missing ticket key",
		})
	}
	if h.client == nil {
		return ctx.Status(fiber.StatusServiceUnavailable).JSON(map[string]interface{}{
			"success": false,
			"message": "Jira integration is not configured",
		})
	}

	issue, err := h.client.GetIssue(ctx.Context(), key, h.githubRepoField, h.githubBaseField, h.githubFeatureField)
	if err != nil {
		return ctx.Status(fiber.StatusBadGateway).JSON(map[string]interface{}{
			"success": false,
			"message": "Failed to fetch ticket: " + err.Error(),
		})
	}

	return ctx.JSON(map[string]interface{}{
		"success": true,
		"data":    issue,
	})
}

func (h *Jira) ExecuteClaudeRunner(ctx *fiber.Ctx) error {
	if h.claudeRunnerURL == "" {
		return ctx.Status(fiber.StatusServiceUnavailable).JSON(map[string]interface{}{
			"success": false,
			"message": "Claude runner is not configured",
		})
	}

	if _, err := strconv.Atoi(ctx.Params("id")); err != nil {
		return ctx.Status(fiber.StatusBadRequest).JSON(map[string]interface{}{
			"success": false,
			"message": "Invalid board ID",
		})
	}

	var req claudeRunnerReq
	if err := ctx.BodyParser(&req); err != nil {
		return ctx.Status(fiber.StatusBadRequest).JSON(map[string]interface{}{
			"success": false,
			"message": "Invalid request body",
		})
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return ctx.Status(fiber.StatusInternalServerError).JSON(map[string]interface{}{
			"success": false,
			"message": "Failed to process request",
		})
	}

	httpClient := &http.Client{Timeout: 120 * time.Second}
	resp, err := httpClient.Post(h.claudeRunnerURL+"/api/executions", "application/json", bytes.NewReader(payload))
	if err != nil {
		return ctx.Status(fiber.StatusBadGateway).JSON(map[string]interface{}{
			"success": false,
			"message": "Failed to reach claude-runner: " + err.Error(),
		})
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	ctx.Set("Content-Type", contentType)
	return ctx.Status(resp.StatusCode).Send(body)
}

func (h *Jira) ExecutionDetailView(ctx *fiber.Ctx) error {
	execID := ctx.Params("id")
	return ctx.Type("html").SendString(fmt.Sprintf(jiraExecutionTemplate, execID, h.claudeRunnerURL))
}

func (h *Jira) GetExecutionData(ctx *fiber.Ctx) error {
	execID := ctx.Params("id")
	if execID == "" {
		return ctx.Status(fiber.StatusBadRequest).JSON(map[string]interface{}{
			"success": false,
			"message": "Missing execution ID",
		})
	}

	proxyReq, err := http.NewRequestWithContext(ctx.Context(), "GET",
		h.claudeRunnerURL+"/api/executions/"+execID, nil)
	if err != nil {
		return ctx.Status(fiber.StatusBadGateway).JSON(map[string]interface{}{
			"success": false,
			"message": "Failed to build request: " + err.Error(),
		})
	}

	proxyClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := proxyClient.Do(proxyReq)
	if err != nil {
		return ctx.Status(fiber.StatusBadGateway).JSON(map[string]interface{}{
			"success": false,
			"message": "Failed to reach claude-runner: " + err.Error(),
		})
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	ctx.Set("Content-Type", "application/json")
	return ctx.Status(resp.StatusCode).Send(body)
}

func (h *Jira) StreamExecutionLogs(ctx *fiber.Ctx) error {
	execID := ctx.Params("id")
	if execID == "" {
		return ctx.Status(fiber.StatusBadRequest).JSON(map[string]interface{}{
			"success": false,
			"message": "Missing execution ID",
		})
	}

	proxyReq, err := http.NewRequestWithContext(ctx.Context(), "GET",
		h.claudeRunnerURL+"/api/executions/"+execID+"/logs", nil)
	if err != nil {
		return ctx.Status(fiber.StatusBadGateway).JSON(map[string]interface{}{
			"success": false,
			"message": "Failed to build request: " + err.Error(),
		})
	}

	proxyClient := &http.Client{Timeout: 0}
	upstream, err := proxyClient.Do(proxyReq)
	if err != nil {
		return ctx.Status(fiber.StatusBadGateway).JSON(map[string]interface{}{
			"success": false,
			"message": "Failed to connect to claude-runner: " + err.Error(),
		})
	}

	ctx.Set("Content-Type", "text/event-stream")
	ctx.Set("Cache-Control", "no-cache")
	ctx.Set("Connection", "keep-alive")
	ctx.Set("X-Accel-Buffering", "no")

	ctx.Context().Response.SetBodyStreamWriter(func(w *bufio.Writer) {
		defer upstream.Body.Close()
		buf := make([]byte, 4096)
		for {
			n, readErr := upstream.Body.Read(buf)
			if n > 0 {
				w.Write(buf[:n]) //nolint:errcheck
				w.Flush()       //nolint:errcheck
			}
			if readErr != nil {
				break
			}
		}
	})
	return nil
}

func (h *Jira) ListExecutions(ctx *fiber.Ctx) error {
	ticketID := ctx.Query("ticketId")
	if ticketID == "" {
		return ctx.Status(fiber.StatusBadRequest).JSON(map[string]interface{}{
			"success": false,
			"message": "ticketId query param required",
		})
	}

	proxyReq, err := http.NewRequestWithContext(ctx.Context(), "GET",
		h.claudeRunnerURL+"/api/executions?ticketId="+ticketID, nil)
	if err != nil {
		return ctx.Status(fiber.StatusBadGateway).JSON(map[string]interface{}{
			"success": false,
			"message": "Failed to build request: " + err.Error(),
		})
	}

	proxyClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := proxyClient.Do(proxyReq)
	if err != nil {
		return ctx.Status(fiber.StatusBadGateway).JSON(map[string]interface{}{
			"success": false,
			"message": "Failed to reach claude-runner: " + err.Error(),
		})
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	ctx.Set("Content-Type", "application/json")
	return ctx.Status(resp.StatusCode).Send(body)
}

func (h *Jira) TicketExecutionsView(ctx *fiber.Ctx) error {
	key := ctx.Params("key")
	return ctx.Type("html").SendString(fmt.Sprintf(jiraTicketExecutionsTemplate, key, h.claudeRunnerURL))
}

func (h *Jira) RootRedirect(ctx *fiber.Ctx) error {
	if h.oauthCfg != nil {
		cookie := ctx.Cookies("_jira_sess")
		if cookie != "" {
			claims := &jiraSessionClaims{}
			_, err := jwt.ParseWithClaims(cookie, claims, func(t *jwt.Token) (interface{}, error) {
				return h.sessionKey, nil
			})
			if err != nil {
				return ctx.Redirect("/jira/login")
			}
		} else {
			return ctx.Redirect("/jira/login")
		}
	}
	return ctx.Redirect("/jira/boards")
}

func (h *Jira) SessionMiddleware() fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		if h.oauthCfg == nil {
			return ctx.Next()
		}
		cookie := ctx.Cookies("_jira_sess")
		if cookie == "" {
			return ctx.Redirect("/jira/login?next=" + ctx.Path())
		}
		claims := &jiraSessionClaims{}
		_, err := jwt.ParseWithClaims(cookie, claims, func(t *jwt.Token) (interface{}, error) {
			return h.sessionKey, nil
		})
		if err != nil {
			ctx.Cookie(&fiber.Cookie{Name: "_jira_sess", Value: "", MaxAge: -1, Path: "/"})
			return ctx.Redirect("/jira/login?next=" + ctx.Path())
		}
		ctx.Locals("jira_email", claims.Email)
		ctx.Locals("jira_name", claims.Name)
		return ctx.Next()
	}
}

func (h *Jira) LoginView(ctx *fiber.Ctx) error {
	if h.oauthCfg == nil {
		return ctx.Redirect("/jira/boards")
	}
	next := ctx.Query("next", "/jira/boards")
	tmpl, err := template.New("login").Parse(jiraLoginTemplate)
	if err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString("template error")
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"Next": next}); err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString("render error")
	}
	ctx.Set("Content-Type", "text/html; charset=utf-8")
	return ctx.Send(buf.Bytes())
}

func (h *Jira) GoogleLogin(ctx *fiber.Ctx) error {
	if h.oauthCfg == nil {
		return ctx.Redirect("/jira/boards")
	}
	b := make([]byte, 16)
	rand.Read(b)
	state := base64.URLEncoding.EncodeToString(b)
	ctx.Cookie(&fiber.Cookie{
		Name:     "_jira_state",
		Value:    state,
		MaxAge:   600,
		Path:     "/",
		HTTPOnly: true,
	})
	next := ctx.Query("next", "/jira/boards")
	url := h.oauthCfg.AuthCodeURL(state, oauth2.SetAuthURLParam("prompt", "select_account")) + "&state_next=" + base64.URLEncoding.EncodeToString([]byte(next))
	return ctx.Redirect(url)
}

func (h *Jira) GoogleCallback(ctx *fiber.Ctx) error {
	if h.oauthCfg == nil {
		return ctx.Redirect("/jira/boards")
	}

	state := ctx.Query("state")
	cookieState := ctx.Cookies("_jira_state")
	if state == "" || state != cookieState {
		return ctx.Status(fiber.StatusBadRequest).SendString("invalid state")
	}
	ctx.Cookie(&fiber.Cookie{Name: "_jira_state", Value: "", MaxAge: -1, Path: "/"})

	code := ctx.Query("code")
	token, err := h.oauthCfg.Exchange(ctx.Context(), code)
	if err != nil {
		return ctx.Status(fiber.StatusUnauthorized).SendString("token exchange failed")
	}

	svc, err := googleoauth2.NewService(ctx.Context(), option.WithTokenSource(h.oauthCfg.TokenSource(ctx.Context(), token)))
	if err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString("failed to create oauth2 service")
	}
	info, err := svc.Userinfo.Get().Do()
	if err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString("failed to get user info")
	}

	if !strings.HasSuffix(info.Email, "@smmf.co.id") {
		return ctx.Status(fiber.StatusForbidden).SendString("access restricted to @smmf.co.id accounts")
	}

	claims := jiraSessionClaims{
		Email: info.Email,
		Name:  info.Name,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	sessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(h.sessionKey)
	if err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString("session error")
	}

	ctx.Cookie(&fiber.Cookie{
		Name:     "_jira_sess",
		Value:    sessToken,
		MaxAge:   8 * 3600,
		Path:     "/",
		HTTPOnly: true,
	})

	return ctx.Redirect("/jira/boards")
}

func (h *Jira) GoogleLogout(ctx *fiber.Ctx) error {
	ctx.Cookie(&fiber.Cookie{Name: "_jira_sess", Value: "", MaxAge: -1, Path: "/"})
	return ctx.Redirect("/jira/login")
}

// @Summary      List Jira boards
// @Tags         jira
// @Produce      json
// @Success      200  {object}  response.BaseResponse
// @Failure      500  {object}  response.BaseResponse
// @Security     BearerAuth
// @Router       /internal/jira/boards [get]
func (h *Jira) GetBoards(ctx *fiber.Ctx) error {
	if h.client == nil {
		return response.BadRequest(ctx, "Jira integration is not configured", nil)
	}
	boards, err := h.client.GetBoards(ctx.Context())
	if err != nil {
		return response.HandleError(ctx, err)
	}
	return response.Success(ctx, boards)
}

// @Summary      Get Jira board summary
// @Tags         jira
// @Produce      json
// @Param        id   path      int  true  "Board ID"
// @Success      200  {object}  response.BaseResponse
// @Failure      400  {object}  response.BaseResponse
// @Failure      500  {object}  response.BaseResponse
// @Security     ApiKeyAuth
// @Router       /internal/jira/boards/{id}/summary [get]
func (h *Jira) GetBoardSummary(ctx *fiber.Ctx) error {
	if h.client == nil {
		return response.BadRequest(ctx, "Jira integration is not configured", nil)
	}
	boardID, err := strconv.Atoi(ctx.Params("id"))
	if err != nil {
		return response.BadRequest(ctx, "invalid board ID", nil)
	}
	summary, err := h.client.GetBoardSummary(ctx.Context(), boardID)
	if err != nil {
		return response.HandleError(ctx, err)
	}
	return response.Success(ctx, summary)
}

func (h *Jira) GetBoardStoryPoints(ctx *fiber.Ctx) error {
	if h.client == nil {
		return response.BadRequest(ctx, "Jira integration is not configured", nil)
	}
	boardID, err := strconv.Atoi(ctx.Params("id"))
	if err != nil {
		return response.BadRequest(ctx, "invalid board ID", nil)
	}
	stats, err := h.client.GetBoardStoryPoints(ctx.Context(), boardID)
	if err != nil {
		return response.HandleError(ctx, err)
	}
	return response.Success(ctx, stats)
}

type BoardWithSprint struct {
	pkgjira.Board
	ActiveSprint *pkgjira.Sprint
	DaysLeft     int
	Overdue      bool
	HasEndDate   bool
	SprintStats  pkgjira.SprintStats
}

func sprintTimeline(sprint *pkgjira.Sprint) (daysLeft int, overdue bool, hasEndDate bool) {
	if sprint == nil || sprint.EndDate == "" {
		return 0, false, false
	}
	end, err := time.Parse("2006-01-02T15:04:05.000Z07:00", sprint.EndDate)
	if err != nil {
		end, err = time.Parse("2006-01-02T15:04:05.000Z", sprint.EndDate)
		if err != nil {
			return 0, false, false
		}
	}
	diff := time.Until(end)
	days := int(math.Ceil(diff.Hours() / 24))
	if days < 0 {
		return int(math.Abs(float64(days))), true, true
	}
	return days, false, true
}

func (h *Jira) BoardsView(ctx *fiber.Ctx) error {
	type viewData struct {
		Boards    []BoardWithSprint
		Error     string
		APIKey    string
		UserName  string
		UserEmail string
	}

	data := viewData{
		APIKey:    h.apiKey,
		UserName:  fmt.Sprintf("%v", ctx.Locals("jira_name")),
		UserEmail: fmt.Sprintf("%v", ctx.Locals("jira_email")),
	}

	if h.client == nil {
		data.Error = "Jira integration is not configured."
	} else {
		boards, err := h.client.GetBoards(ctx.Context())
		if err != nil {
			data.Error = "Failed to fetch boards from Jira: " + err.Error()
		} else {
			results := make([]BoardWithSprint, len(boards))
			var wg sync.WaitGroup
			for i, b := range boards {
				results[i] = BoardWithSprint{Board: b}
				wg.Add(1)
				go func(idx int, boardID int, boardType string) {
					defer wg.Done()
					if boardType == "scrum" {
						sprint, _ := h.client.GetActiveSprint(ctx.Context(), boardID)
						results[idx].ActiveSprint = sprint
						results[idx].DaysLeft, results[idx].Overdue, results[idx].HasEndDate = sprintTimeline(sprint)
						if sprint != nil {
							stats, _ := h.client.GetSprintStats(ctx.Context(), sprint.ID)
							results[idx].SprintStats = stats
						}
					}
				}(i, b.ID, b.Type)
			}
			wg.Wait()
			data.Boards = results
		}
	}

	tmpl, err := template.New("boards").Funcs(template.FuncMap{
		"pct": func(done, total int) int {
			if total == 0 {
				return 0
			}
			return int(math.Round(float64(done) / float64(total) * 100))
		},
	}).Parse(boardsTemplate)
	if err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString("template error: " + err.Error())
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString("render error: " + err.Error())
	}

	ctx.Set("Content-Type", "text/html; charset=utf-8")
	return ctx.Send(buf.Bytes())
}

var boardsTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Jira Boards</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <script>tailwind.config={darkMode:'class'}</script>
  <script>(function(){var t=localStorage.getItem('theme');if(t==='dark'||(!t&&window.matchMedia('(prefers-color-scheme:dark)').matches))document.documentElement.classList.add('dark');})()</script>
  <style>
    .filter-btn { background: white; border-color: #e5e7eb; color: #6b7280; }
    .filter-btn:hover { border-color: #3b82f6; color: #3b82f6; }
    .filter-btn.active { background: #3b82f6; border-color: #3b82f6; color: white; }
    .dark .filter-btn { background: #1f2937; border-color: #374151; color: #9ca3af; }
    .dark .filter-btn:hover { border-color: #60a5fa; color: #60a5fa; }
    .dark .filter-btn.active { background: #3b82f6; border-color: #3b82f6; color: white; }
  </style>
</head>
<body class="bg-gray-50 dark:bg-gray-900 min-h-screen">

  <div class="max-w-5xl mx-auto px-6 py-10">

    <!-- Header -->
    <div class="flex items-center justify-between gap-3 mb-8">
      <div class="flex items-center gap-3">
        <svg class="w-8 h-8 text-blue-600" fill="currentColor" viewBox="0 0 24 24">
          <path d="M11.571 11.513H0a5.218 5.218 0 0 0 5.232 5.215h2.13v2.057A5.215 5.215 0 0 0 12.575 24V12.518a1.005 1.005 0 0 0-1.004-1.005zm5.723-5.756H5.757a5.215 5.215 0 0 0 5.215 5.214h2.129v2.058a5.218 5.218 0 0 0 5.215 5.214V6.762a1.005 1.005 0 0 0-1.022-1.005zM23.013 0H11.455a5.215 5.215 0 0 0 5.215 5.215h2.129v2.057A5.215 5.215 0 0 0 24 12.486V1.005A1.005 1.005 0 0 0 23.013 0z"/>
        </svg>
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-gray-100">Jira Boards</h1>
          <p class="text-sm text-gray-500 dark:text-gray-400">All boards from your Jira workspace</p>
        </div>
      </div>
      <div class="flex items-center gap-3">
        <button onclick="toggleTheme()" class="p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors" title="Toggle dark mode">
          <svg class="w-4 h-4 text-gray-500 dark:text-gray-400 dark:hidden" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z"/></svg>
          <svg class="w-4 h-4 text-gray-500 dark:text-gray-400 hidden dark:block" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364-6.364l-.707.707M6.343 17.657l-.707.707M17.657 17.657l-.707-.707M6.343 6.343l-.707-.707M16 12a4 4 0 11-8 0 4 4 0 018 0z"/></svg>
        </button>
        {{if .UserEmail}}
        <div class="flex items-center gap-3">
          <div class="text-right">
            <p class="text-sm font-medium text-gray-700 dark:text-gray-300">{{.UserName}}</p>
            <p class="text-xs text-gray-400 dark:text-gray-500">{{.UserEmail}}</p>
          </div>
          <a href="/jira/logout"
             class="inline-flex items-center gap-1.5 text-xs font-medium text-gray-500 hover:text-red-600 border border-gray-200 dark:border-gray-700 hover:border-red-200 px-3 py-1.5 rounded-lg transition-colors dark:text-gray-400">
            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1"/>
            </svg>
            Logout
          </a>
        </div>
        {{end}}
      </div>
    </div>

    <!-- Error state -->
    {{if .Error}}
    <div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-4 flex items-start gap-3">
      <svg class="w-5 h-5 text-red-500 mt-0.5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/>
      </svg>
      <p class="text-sm text-red-700 dark:text-red-400">{{.Error}}</p>
    </div>
    {{end}}

    <!-- Empty state -->
    {{if and (not .Error) (eq (len .Boards) 0)}}
    <div class="text-center py-20 text-gray-400 dark:text-gray-500">
      <svg class="w-12 h-12 mx-auto mb-4 opacity-40" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M9 17V7m0 10a2 2 0 01-2 2H5a2 2 0 01-2-2V7a2 2 0 012-2h2a2 2 0 012 2m0 10a2 2 0 002 2h2a2 2 0 002-2M9 7a2 2 0 012-2h2a2 2 0 012 2m0 10V7"/>
      </svg>
      <p class="text-sm font-medium">No boards found</p>
    </div>
    {{end}}

    <!-- Search + Filter bar -->
    {{if .Boards}}
    <div class="flex flex-col sm:flex-row gap-3 mb-4">
      <div class="relative flex-1">
        <svg class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400 pointer-events-none" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-4.35-4.35M17 11A6 6 0 1 1 5 11a6 6 0 0 1 12 0z"/>
        </svg>
        <input id="search-input" type="text" placeholder="Search by name or ID..."
               oninput="applyFilters()"
               class="w-full pl-10 pr-4 py-2.5 text-sm bg-white dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 border border-gray-200 rounded-xl shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent placeholder-gray-400 dark:placeholder-gray-500" />
      </div>
      <div class="flex items-center gap-2 flex-wrap">
        <span class="text-xs text-gray-400 dark:text-gray-500 font-medium">Type</span>
        <button onclick="setType('all')"    id="filter-all"    class="filter-btn px-3 py-2 text-sm font-medium rounded-xl border transition-colors">All</button>
        <button onclick="setType('scrum')"  id="filter-scrum"  class="filter-btn active px-3 py-2 text-sm font-medium rounded-xl border transition-colors">Scrum</button>
        <button onclick="setType('kanban')" id="filter-kanban" class="filter-btn px-3 py-2 text-sm font-medium rounded-xl border transition-colors">Kanban</button>

        <span class="text-xs text-gray-400 dark:text-gray-500 font-medium ml-2">Sprint</span>
        <button onclick="setSprint('all')"      id="sprint-all"      class="filter-btn sprint-btn px-3 py-2 text-sm font-medium rounded-xl border transition-colors">All</button>
        <button onclick="setSprint('active')"   id="sprint-active"   class="filter-btn sprint-btn active px-3 py-2 text-sm font-medium rounded-xl border transition-colors">Active</button>
        <button onclick="setSprint('overdue')"  id="sprint-overdue"  class="filter-btn sprint-btn px-3 py-2 text-sm font-medium rounded-xl border transition-colors">Overdue</button>
        <button onclick="setSprint('none')"     id="sprint-none"     class="filter-btn sprint-btn px-3 py-2 text-sm font-medium rounded-xl border transition-colors">No Sprint</button>
      </div>
    </div>
    <div class="flex items-center justify-between mb-4">
      <p id="board-count" class="text-sm text-gray-500 dark:text-gray-400">{{len .Boards}} board{{if gt (len .Boards) 1}}s{{end}} found</p>
      <div class="flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
        <span>Per page:</span>
        <select id="page-size" onchange="changePageSize()" class="text-sm bg-white dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 border border-gray-200 rounded-lg px-2 py-1 focus:outline-none focus:ring-2 focus:ring-blue-500">
          <option value="12" selected>12</option>
          <option value="24">24</option>
          <option value="48">48</option>
        </select>
      </div>
    </div>
    {{end}}

    <!-- Board grid -->
    <div id="board-grid" class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
      {{range .Boards}}
      <div onclick="openSummary({{.ID}}, '{{.Name}}')"
           data-name="{{.Name}}" data-type="{{.Type}}" data-id="{{.ID}}"
           data-sprint="{{if .ActiveSprint}}{{if .Overdue}}overdue{{else}}active{{end}}{{else}}none{{end}}"
           class="bg-white dark:bg-gray-800 rounded-xl border border-gray-200 dark:border-gray-700 shadow-sm hover:shadow-md hover:border-blue-300 transition-all cursor-pointer p-5 group">
        <div class="flex items-start justify-between gap-2 mb-3">
          <h2 class="text-base font-semibold text-gray-800 dark:text-gray-200 group-hover:text-blue-600 leading-snug transition-colors">{{.Name}}</h2>
          {{if eq .Type "scrum"}}
          <span class="text-xs font-medium bg-blue-100 text-blue-700 px-2 py-0.5 rounded-full whitespace-nowrap">Scrum</span>
          {{else if eq .Type "kanban"}}
          <span class="text-xs font-medium bg-green-100 text-green-700 px-2 py-0.5 rounded-full whitespace-nowrap">Kanban</span>
          {{else}}
          <span class="text-xs font-medium bg-gray-100 dark:bg-gray-700 text-gray-600 dark:text-gray-300 px-2 py-0.5 rounded-full whitespace-nowrap">{{.Type}}</span>
          {{end}}
        </div>
        {{if .ActiveSprint}}
        {{$total := .SprintStats.Total}}
        {{$done  := .SprintStats.Done}}
        {{if gt $total 0}}
        <div class="mt-3 mb-1">
          <div class="flex items-center justify-between mb-1.5">
            <span class="text-xs text-gray-500 dark:text-gray-400">{{$done}}/{{$total}} done</span>
            <span class="text-xs font-medium {{if eq $done $total}}text-emerald-600{{else}}text-gray-400 dark:text-gray-500{{end}}">
              {{if gt $total 0}}{{pct $done $total}}%{{end}}
            </span>
          </div>
          <div class="w-full h-1.5 bg-gray-100 dark:bg-gray-700 rounded-full overflow-hidden">
            <div class="h-full rounded-full {{if eq $done $total}}bg-emerald-500{{else if .Overdue}}bg-red-400{{else}}bg-blue-500{{end}} transition-all"
                 style="width: {{pct $done $total}}%"></div>
          </div>
        </div>
        {{end}}
        {{end}}
        <div id="sp-{{.ID}}" class="flex items-center gap-2 mt-2">
          <svg class="animate-spin h-3 w-3 text-gray-400" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"></path>
          </svg>
        </div>
        <div class="flex items-center justify-between mt-2">
          <p class="text-xs text-gray-400 dark:text-gray-500">ID: {{.ID}}</p>
          {{if .ActiveSprint}}
            {{if .Overdue}}
            <span class="inline-flex items-center gap-1 text-xs font-medium bg-red-50 text-red-700 border border-red-200 px-2 py-0.5 rounded-full" title="{{.ActiveSprint.Name}}">
              <span class="w-1.5 h-1.5 rounded-full bg-red-500"></span>
              {{if .HasEndDate}}{{.DaysLeft}}d overdue{{else}}{{.ActiveSprint.Name}}{{end}}
            </span>
            {{else if .HasEndDate}}
            <span class="inline-flex items-center gap-1 text-xs font-medium bg-emerald-50 text-emerald-700 border border-emerald-200 px-2 py-0.5 rounded-full" title="{{.ActiveSprint.Name}}">
              <span class="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse"></span>
              {{.DaysLeft}}d left
            </span>
            {{else}}
            <span class="inline-flex items-center gap-1 text-xs font-medium bg-emerald-50 text-emerald-700 border border-emerald-200 px-2 py-0.5 rounded-full">
              <span class="w-1.5 h-1.5 rounded-full bg-emerald-500"></span>
              {{.ActiveSprint.Name}}
            </span>
            {{end}}
          {{else if eq .Type "scrum"}}
          <span class="text-xs text-gray-400 dark:text-gray-500 italic">No active sprint</span>
          {{end}}
        </div>
      </div>
      {{end}}
    </div>

    <!-- Pagination -->
    <div id="pagination" class="flex items-center justify-center gap-1 mt-6"></div>

  </div>

  <!-- Modal backdrop -->
  <div id="modal-backdrop" onclick="closeModal()"
       class="fixed inset-0 bg-black/40 z-40 hidden opacity-0 transition-opacity duration-200"></div>

  <!-- Summary modal -->
  <div id="summary-modal"
       class="fixed right-0 top-0 h-full w-full max-w-lg bg-white dark:bg-gray-800 shadow-2xl z-50 translate-x-full transition-transform duration-300 overflow-y-auto">

    <div class="sticky top-0 bg-white dark:bg-gray-800 border-b border-gray-100 dark:border-gray-700 px-6 py-4 flex items-center justify-between z-10">
      <div>
        <h2 id="modal-title" class="text-lg font-semibold text-gray-900 dark:text-gray-100"></h2>
        <p class="text-xs text-gray-400 dark:text-gray-500 mt-0.5">Board Summary</p>
      </div>
      <button onclick="closeModal()" class="p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors">
        <svg class="w-5 h-5 text-gray-500 dark:text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
        </svg>
      </button>
    </div>

    <!-- Loading -->
    <div id="modal-loading" class="flex flex-col items-center justify-center py-24 gap-3 text-gray-400 dark:text-gray-500">
      <svg class="w-8 h-8 animate-spin" fill="none" viewBox="0 0 24 24">
        <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/>
        <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z"/>
      </svg>
      <p class="text-sm">Loading summary...</p>
    </div>

    <!-- Error -->
    <div id="modal-error" class="hidden px-6 py-8">
      <div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-4 text-sm text-red-700 dark:text-red-400"></div>
    </div>

    <!-- Content -->
    <div id="modal-content" class="hidden px-6 py-6 space-y-6"></div>
  </div>

  <script>
    const API_KEY = '{{.APIKey}}';

    let activeType   = 'scrum';
    let activeSprint = 'active';
    let currentPage  = 1;
    let pageSize     = 12;
    let filteredCards = [];

    function setType(type) {
      activeType = type;
      document.querySelectorAll('.filter-btn:not(.sprint-btn)').forEach(b => b.classList.remove('active'));
      document.getElementById('filter-' + type).classList.add('active');
      applyFilters();
    }

    function setSprint(sprint) {
      activeSprint = sprint;
      document.querySelectorAll('.sprint-btn').forEach(b => b.classList.remove('active'));
      document.getElementById('sprint-' + sprint).classList.add('active');
      applyFilters();
    }

    function changePageSize() {
      pageSize = parseInt(document.getElementById('page-size').value);
      currentPage = 1;
      renderPage();
    }

    function applyFilters() {
      const q = (document.getElementById('search-input').value || '').toLowerCase().trim();
      const cards = document.querySelectorAll('#board-grid [data-name]');
      filteredCards = [];
      cards.forEach(card => {
        const name   = card.dataset.name.toLowerCase();
        const type   = card.dataset.type.toLowerCase();
        const sprint = card.dataset.sprint;
        const id     = card.dataset.id || '';
        const match  = (!q || name.includes(q) || id === q)
                    && (activeType   === 'all' || type   === activeType)
                    && (activeSprint === 'all' || sprint === activeSprint);
        card.style.display = 'none';
        if (match) filteredCards.push(card);
      });
      currentPage = 1;
      renderPage();
    }

    function renderPage() {
      const total = filteredCards.length;
      const totalPages = Math.max(1, Math.ceil(total / pageSize));
      if (currentPage > totalPages) currentPage = totalPages;
      const start = (currentPage - 1) * pageSize;
      const end   = start + pageSize;

      filteredCards.forEach((card, i) => {
        card.style.display = (i >= start && i < end) ? '' : 'none';
      });

      const countEl = document.getElementById('board-count');
      if (countEl) {
        const showing = Math.min(end, total) - start;
        countEl.textContent = total + ' board' + (total !== 1 ? 's' : '') + ' found'
          + (totalPages > 1 ? ' — showing ' + (start + 1) + '–' + Math.min(end, total) : '');
      }

      renderPagination(totalPages);
    }

    function renderPagination(totalPages) {
      const el = document.getElementById('pagination');
      if (!el) return;
      if (totalPages <= 1) { el.innerHTML = ''; return; }

      const btnBase = 'px-3 py-1.5 text-sm rounded-lg border transition-colors';
      const btnActive = 'bg-blue-500 border-blue-500 text-white font-semibold';
      const btnInactive = 'bg-white dark:bg-gray-800 border-gray-200 dark:border-gray-700 text-gray-600 dark:text-gray-300 hover:border-blue-400 hover:text-blue-600';
      const btnDisabled = 'bg-white dark:bg-gray-800 border-gray-100 dark:border-gray-700 text-gray-300 dark:text-gray-600 cursor-not-allowed';

      let html = '';

      html += '<button onclick="goPage(' + (currentPage - 1) + ')" ' + (currentPage === 1 ? 'disabled' : '') + ' class="' + btnBase + ' ' + (currentPage === 1 ? btnDisabled : btnInactive) + '">‹ Prev</button>';

      const pages = pagesToShow(currentPage, totalPages);
      let prev = null;
      for (const p of pages) {
        if (prev !== null && p - prev > 1) {
          html += '<span class="px-2 py-1.5 text-sm text-gray-400">…</span>';
        }
        if (p === currentPage) {
          html += '<button class="' + btnBase + ' ' + btnActive + '">' + p + '</button>';
        } else {
          html += '<button onclick="goPage(' + p + ')" class="' + btnBase + ' ' + btnInactive + '">' + p + '</button>';
        }
        prev = p;
      }

      html += '<button onclick="goPage(' + (currentPage + 1) + ')" ' + (currentPage === totalPages ? 'disabled' : '') + ' class="' + btnBase + ' ' + (currentPage === totalPages ? btnDisabled : btnInactive) + '">Next ›</button>';

      el.innerHTML = html;
    }

    function pagesToShow(current, total) {
      const pages = new Set([1, total]);
      for (let p = Math.max(1, current - 2); p <= Math.min(total, current + 2); p++) pages.add(p);
      return [...pages].sort((a, b) => a - b);
    }

    function goPage(p) {
      const totalPages = Math.max(1, Math.ceil(filteredCards.length / pageSize));
      if (p < 1 || p > totalPages) return;
      currentPage = p;
      renderPage();
      window.scrollTo({ top: 0, behavior: 'smooth' });
    }

    function spFloat(f) {
      return Number.isInteger(f) ? f + ' SP' : f.toFixed(1) + ' SP';
    }

    function loadBoardStoryPoints() {
      document.querySelectorAll('#board-grid [data-id]').forEach(function(card) {
        var boardId = card.dataset.id;
        var el = document.getElementById('sp-' + boardId);
        if (!el) return;
        fetch('/partner/v1/jira/boards/' + boardId + '/story-points', {
          headers: { 'X-API-Key': API_KEY }
        })
        .then(function(r) { return r.ok ? r.json() : null; })
        .then(function(res) {
          var data = res && res.data;
          if (!data || data.total_sp <= 0) { el.innerHTML = ''; return; }
          var html = '<span class="inline-flex items-center gap-1 text-xs font-medium bg-purple-50 dark:bg-purple-900/20 text-purple-700 dark:text-purple-400 border border-purple-100 dark:border-purple-800/50 px-2 py-0.5 rounded-full" title="Total story points for all tickets">' + spFloat(data.total_sp) + ' total</span>';
          if (data.story_sp > 0) {
            html += '<span class="inline-flex items-center gap-1 text-xs font-medium bg-violet-50 dark:bg-violet-900/20 text-violet-700 dark:text-violet-400 border border-violet-100 dark:border-violet-800/50 px-2 py-0.5 rounded-full" title="Story points for Story-type tickets">' + spFloat(data.story_sp) + ' stories</span>';
          }
          el.innerHTML = html;
        })
        .catch(function() { el.innerHTML = ''; });
      });
    }

    // initialise on load
    window.addEventListener('DOMContentLoaded', function() { applyFilters(); loadBoardStoryPoints(); });

    function openSummary(boardId, boardName) {
      document.getElementById('modal-title').textContent = boardName;
      document.getElementById('modal-loading').classList.remove('hidden');
      document.getElementById('modal-error').classList.add('hidden');
      document.getElementById('modal-content').classList.add('hidden');

      const backdrop = document.getElementById('modal-backdrop');
      const modal = document.getElementById('summary-modal');
      backdrop.classList.remove('hidden');
      setTimeout(() => {
        backdrop.classList.remove('opacity-0');
        modal.classList.remove('translate-x-full');
      }, 10);

      fetch('/partner/v1/jira/boards/' + boardId + '/summary', {
        headers: { 'X-Api-Key': API_KEY }
      })
      .then(r => r.json())
      .then(res => {
        if (!res.success) throw new Error(res.message || 'Unknown error');
        renderSummary(res.data);
      })
      .catch(err => {
        document.getElementById('modal-loading').classList.add('hidden');
        const errEl = document.getElementById('modal-error');
        errEl.classList.remove('hidden');
        errEl.querySelector('div').textContent = 'Failed to load summary: ' + err.message;
      });
    }

    function closeModal() {
      const backdrop = document.getElementById('modal-backdrop');
      const modal = document.getElementById('summary-modal');
      backdrop.classList.add('opacity-0');
      modal.classList.add('translate-x-full');
      setTimeout(() => backdrop.classList.add('hidden'), 200);
    }

    function esc(s) { return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;'); }

    function renderSummary(data) {
      document.getElementById('modal-loading').classList.add('hidden');
      const content = document.getElementById('modal-content');
      content.classList.remove('hidden');

      let html = '';

      // Board type badge
      const typeBadge = data.board.type === 'scrum'
        ? '<span class="text-xs font-medium bg-blue-100 text-blue-700 px-2 py-0.5 rounded-full">Scrum</span>'
        : '<span class="text-xs font-medium bg-green-100 text-green-700 px-2 py-0.5 rounded-full">Kanban</span>';
      html += '<div class="flex items-center gap-2">' + typeBadge + '<span class="text-xs text-gray-400 dark:text-gray-500">Board ID: ' + data.board.id + '</span></div>';

      // Active sprint
      if (data.active_sprint) {
        const s = data.active_sprint;
        const ss = data.sprint_stats || {};
        const total = ss.Total || 0;
        const done  = ss.Done  || 0;
        const pct   = total > 0 ? Math.round(done / total * 100) : 0;
        const isFuture  = s.state === 'future';
        const isOverdue = !isFuture && s.endDate && new Date(s.endDate) < new Date();
        const barColor  = pct === 100 ? 'bg-emerald-500' : isOverdue ? 'bg-red-400' : 'bg-blue-500';

        const overdueLabel = isFuture
          ? '<span class="text-xs font-medium bg-gray-100 text-gray-500 px-2 py-1 rounded-full whitespace-nowrap">Not Started</span>'
          : isOverdue
            ? '<span class="text-xs font-medium bg-red-100 text-red-600 px-2 py-1 rounded-full whitespace-nowrap">Overdue</span>'
            : '<span class="text-xs font-medium bg-emerald-100 text-emerald-700 px-2 py-1 rounded-full whitespace-nowrap">On Track</span>';
        const goalHtml   = s.goal      ? '<p class="text-sm text-gray-600 dark:text-gray-400 italic">&ldquo;' + esc(s.goal) + '&rdquo;</p>' : '';
        const startHtml  = s.startDate ? '<span>Start: ' + s.startDate.slice(0,10) + '</span>' : '';
        const endHtml    = s.endDate   ? '<span>End: '   + s.endDate.slice(0,10)   + '</span>' : '';
        const pctColor   = pct === 100 ? 'text-emerald-600' : 'text-gray-500';
        const progressHtml = total > 0
          ? '<div><div class="flex justify-between text-xs mb-1.5"><span class="text-gray-600 dark:text-gray-300 font-medium">' + done + '/' + total + ' done</span><span class="font-semibold ' + pctColor + '">' + pct + '%</span></div><div class="w-full h-2 bg-white dark:bg-gray-700 rounded-full overflow-hidden border border-blue-100 dark:border-blue-900"><div class="h-full rounded-full ' + barColor + ' transition-all" style="width:' + pct + '%"></div></div></div>'
          : '';

        const sprintLabel = s.state === 'future' ? 'Future Sprint' : 'Active Sprint';
        html += '<div class="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded-xl p-4 space-y-3">'
          + '<div class="flex items-start justify-between gap-2"><div>'
          + '<p class="text-xs font-semibold text-blue-500 uppercase tracking-wide">' + sprintLabel + '</p>'
          + '<p class="text-base font-semibold text-gray-800 dark:text-gray-200 mt-0.5">' + esc(s.name) + '</p>'
          + '</div>' + overdueLabel + '</div>'
          + goalHtml
          + '<div class="flex gap-4 text-xs text-gray-500 dark:text-gray-400">' + startHtml + endHtml + '</div>'
          + progressHtml
          + '</div>';
      }

      // Build per-assignee maps for Overall Tasks tooltips
      const overallStatusAssigneesMap = {};
      const overallTypeAssigneesMap = {};
      (data.issues || []).forEach(function(i) {
        if (!i.fields.assignee) return;
        const name = i.fields.assignee.displayName;
        const sName = i.fields.status.name;
        const tName = (i.fields.issuetype && i.fields.issuetype.name) || 'Unknown';
        if (!overallStatusAssigneesMap[sName]) overallStatusAssigneesMap[sName] = {};
        overallStatusAssigneesMap[sName][name] = (overallStatusAssigneesMap[sName][name] || 0) + 1;
        if (!overallTypeAssigneesMap[tName]) overallTypeAssigneesMap[tName] = {};
        overallTypeAssigneesMap[tName][name] = (overallTypeAssigneesMap[tName][name] || 0) + 1;
      });

      // Status breakdown
      if (data.status_stats && Object.keys(data.status_stats).length > 0) {
        const statusColor = s => ({
          'To Do': 'bg-gray-100 text-gray-600',
          'In Progress': 'bg-blue-100 text-blue-700',
          'In Review': 'bg-yellow-100 text-yellow-700',
          'Done': 'bg-emerald-100 text-emerald-700',
        }[s] || 'bg-gray-100 text-gray-500');

        html += '<div><p class="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wide mb-2">By Status <span class="normal-case font-normal text-gray-400 dark:text-gray-500">(' + data.total_issues + ' total)</span></p><div class="flex flex-wrap gap-2">';
        for (const [s, n] of Object.entries(data.status_stats)) {
          const sa = JSON.stringify(overallStatusAssigneesMap[s] || {}).replace(/'/g, '&#39;');
          html += '<span class="text-xs font-medium px-2.5 py-1 rounded-full cursor-default ' + statusColor(s) + '" data-assignees=\'' + sa + '\' onmouseenter="showPillTooltip(event)" onmouseleave="hidePillTooltip()">' + esc(s) + ': ' + n + '</span>';
        }
        html += '</div>';
        html += '<div class="mt-2"><a href="/jira/boards/' + data.board.id + '/issues" class="inline-flex items-center gap-1.5 text-xs font-semibold text-gray-500 hover:text-blue-600 transition-colors">View Full List<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M9 5l7 7-7 7"/></svg></a></div>';
        html += '</div>';
      }

      // Type breakdown
      if (data.type_stats && Object.keys(data.type_stats).length > 0) {
        const typeColor = t => ({
          'Bug': 'bg-red-50 text-red-600 border-red-200',
          'Story': 'bg-purple-50 text-purple-600 border-purple-200',
          'Task': 'bg-blue-50 text-blue-600 border-blue-200',
          'Epic': 'bg-orange-50 text-orange-600 border-orange-200',
          'Sub-task': 'bg-gray-50 text-gray-600 border-gray-200',
        }[t] || 'bg-gray-50 text-gray-500 border-gray-200');

        html += '<div><p class="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wide mb-2">By Type</p><div class="flex flex-wrap gap-2">';
        for (const [t, n] of Object.entries(data.type_stats)) {
          const ta = JSON.stringify(overallTypeAssigneesMap[t] || {}).replace(/'/g, '&#39;');
          html += '<span class="text-xs font-medium px-2.5 py-1 rounded-full border cursor-default ' + typeColor(t) + '" data-assignees=\'' + ta + '\' onmouseenter="showPillTooltip(event)" onmouseleave="hidePillTooltip()">' + esc(t) + ': ' + n + '</span>';
        }
        html += '</div></div>';
      }

      // Assignee breakdown
      if (data.assignee_stats && data.assignee_stats.length > 0) {
        const sorted = [...data.assignee_stats].sort((a, b) => b.story_points - a.story_points);
        html += '<div><p class="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wide mb-2">By Assignee</p><div class="space-y-2">';
        sorted.forEach(function(a) {
          const initials = a.display_name.split(' ').map(function(w){return w[0];}).join('').slice(0,2).toUpperCase();
          const avatar = a.avatar_url
            ? '<img src="' + esc(a.avatar_url) + '" class="w-7 h-7 rounded-full object-cover" />'
            : '<div class="w-7 h-7 rounded-full bg-blue-100 text-blue-700 text-xs font-bold flex items-center justify-center">' + initials + '</div>';
          html += '<div class="flex items-center gap-3">' + avatar
            + '<span class="text-sm text-gray-700 dark:text-gray-300 flex-1">' + esc(a.display_name) + '</span>'
            + '<span class="text-xs font-medium text-purple-700 bg-purple-100 px-2 py-0.5 rounded-full" title="Story Points">' + a.story_points + ' SP</span>'
            + '<span class="text-xs font-semibold text-gray-500 dark:text-gray-400 bg-gray-100 dark:bg-gray-700 px-2 py-0.5 rounded-full">' + a.count + '</span>'
            + '</div>';
        });
        html += '</div></div>';
      }

      // Remaining work breakdown (undone tasks only)
      const allIssues = data.issues || [];
      const undone = allIssues.filter(function(i) {
        return i.fields.status.statusCategory.key !== 'done';
      });

      if (allIssues.length > 0) {
        const undoneByStatus = {};
        const undoneByType = {};
        const undoneAssigneeMap = {};
        const statusAssigneesMap = {};
        const typeAssigneesMap = {};

        undone.forEach(function(i) {
          const sName = i.fields.status.name;
          undoneByStatus[sName] = (undoneByStatus[sName] || 0) + 1;
          const tName = (i.fields.issuetype && i.fields.issuetype.name) || 'Unknown';
          undoneByType[tName] = (undoneByType[tName] || 0) + 1;
          if (i.fields.assignee) {
            const name = i.fields.assignee.displayName;
            if (!undoneAssigneeMap[name]) {
              let av = '';
              if (i.fields.assignee.avatarUrls && i.fields.assignee.avatarUrls['32x32']) {
                av = i.fields.assignee.avatarUrls['32x32'];
              }
              undoneAssigneeMap[name] = { display_name: name, avatar_url: av, count: 0, story_points: 0 };
            }
            undoneAssigneeMap[name].count++;
            const sp = i.fields.customfield_10032;
            if (sp != null && sp > 0) undoneAssigneeMap[name].story_points += sp;
            if (!statusAssigneesMap[sName]) statusAssigneesMap[sName] = {};
            statusAssigneesMap[sName][name] = (statusAssigneesMap[sName][name] || 0) + 1;
            if (!typeAssigneesMap[tName]) typeAssigneesMap[tName] = {};
            typeAssigneesMap[tName][name] = (typeAssigneesMap[tName][name] || 0) + 1;
          }
        });

        const statusColor = function(s) {
          return ({
            'In Progress': 'bg-blue-100 text-blue-700',
            'In Review':   'bg-yellow-100 text-yellow-700',
            'To Do':       'bg-gray-100 text-gray-600',
          }[s] || 'bg-gray-100 text-gray-500');
        };
        const typeColor = function(t) {
          return ({
            'Bug':      'bg-red-50 text-red-600 border-red-200',
            'Story':    'bg-purple-50 text-purple-600 border-purple-200',
            'Task':     'bg-blue-50 text-blue-600 border-blue-200',
            'Epic':     'bg-orange-50 text-orange-600 border-orange-200',
            'Sub-task': 'bg-gray-50 text-gray-600 border-gray-200',
          }[t] || 'bg-gray-50 text-gray-500 border-gray-200');
        };

        let statusPills = '';
        for (const entry of Object.entries(undoneByStatus)) {
          const sa = JSON.stringify(statusAssigneesMap[entry[0]] || []).replace(/'/g, '&#39;');
          statusPills += '<span class="text-xs font-medium px-2.5 py-1 rounded-full cursor-default ' + statusColor(entry[0]) + '" data-assignees=\'' + sa + '\' onmouseenter="showPillTooltip(event)" onmouseleave="hidePillTooltip()">' + esc(entry[0]) + ': ' + entry[1] + '</span>';
        }
        let typePills = '';
        for (const entry of Object.entries(undoneByType)) {
          const ta = JSON.stringify(typeAssigneesMap[entry[0]] || []).replace(/'/g, '&#39;');
          typePills += '<span class="text-xs font-medium px-2.5 py-1 rounded-full border cursor-default ' + typeColor(entry[0]) + '" data-assignees=\'' + ta + '\' onmouseenter="showPillTooltip(event)" onmouseleave="hidePillTooltip()">' + esc(entry[0]) + ': ' + entry[1] + '</span>';
        }

        html += '<div class="bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 rounded-xl p-4 space-y-3">'
          + '<div class="flex items-center justify-between">'
          + '<p class="text-xs font-semibold text-amber-700 uppercase tracking-wide">Remaining Work</p>'
          + '<span class="text-sm font-bold text-amber-700">' + undone.length + ' <span class="font-normal text-amber-500 text-xs">of ' + allIssues.length + ' shown</span></span>'
          + '</div>';
        if (statusPills) {
          html += '<div><p class="text-xs text-amber-600 font-medium mb-1.5">By Status</p><div class="flex flex-wrap gap-1.5">' + statusPills + '</div></div>';
        }
        if (typePills) {
          html += '<div><p class="text-xs text-amber-600 font-medium mb-1.5">By Type</p><div class="flex flex-wrap gap-1.5">' + typePills + '</div></div>';
        }

        // Assignee breakdown inside the amber card
        const undoneAssignees = Object.values(undoneAssigneeMap).sort(function(a, b) { return b.story_points - a.story_points; });
        if (undoneAssignees.length > 0) {
          html += '<div><p class="text-xs text-amber-600 font-medium mb-1.5">By Assignee</p><div class="space-y-2">';
          undoneAssignees.forEach(function(a) {
            const initials = a.display_name.split(' ').map(function(w) { return w[0]; }).join('').slice(0, 2).toUpperCase();
            const avatar = a.avatar_url
              ? '<img src="' + esc(a.avatar_url) + '" class="w-7 h-7 rounded-full object-cover" />'
              : '<div class="w-7 h-7 rounded-full bg-amber-100 text-amber-700 text-xs font-bold flex items-center justify-center">' + initials + '</div>';
            html += '<div class="flex items-center gap-3">' + avatar
              + '<span class="text-sm text-gray-700 dark:text-gray-300 flex-1">' + esc(a.display_name) + '</span>'
              + '<span class="text-xs font-medium text-purple-700 bg-purple-100 px-2 py-0.5 rounded-full" title="Story Points">' + a.story_points + ' SP</span>'
              + '<span class="text-xs font-semibold text-amber-700 bg-amber-100 px-2 py-0.5 rounded-full">' + a.count + '</span>'
              + '</div>';
          });
          html += '</div></div>';
        }

        html += '<div class="border-t border-amber-200 dark:border-amber-800 pt-3 mt-1">'
          + '<a href="/jira/boards/' + data.board.id + '/remaining" class="inline-flex items-center gap-1.5 text-xs font-semibold text-amber-700 hover:text-amber-900 transition-colors">'
          + 'View full remaining list'
          + '<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M9 5l7 7-7 7"/></svg>'
          + '</a></div>';

        html += '</div>';
      }

      // Issues list
      if (data.issues && data.issues.length > 0) {
        html += '<div id="issues-section">'
          + '<div class="flex items-center justify-between mb-2">'
          + '<p class="text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wide">Issues</p>'
          + '<span id="issues-count" class="text-xs text-gray-400 dark:text-gray-500"></span>'
          + '</div>'
          + '<div id="issues-list" class="space-y-1.5"></div>'
          + '<div id="issues-pagination" class="flex items-center justify-center gap-1 mt-3"></div>'
          + '</div>';

        content.innerHTML = html;
        initIssuesPagination(data.issues);
        return;
      }

      if (!html) {
        html = '<p class="text-sm text-gray-400 dark:text-gray-500 text-center py-8">No data available for this board.</p>';
      }

      content.innerHTML = html;
    }
    function initIssuesPagination(issues) {
      const perPage = 10;
      let page = 1;
      const totalPages = () => Math.ceil(issues.length / perPage);

      const priorityColor = p => ({ 'Highest': 'text-red-600', 'High': 'text-orange-500', 'Medium': 'text-yellow-500', 'Low': 'text-blue-400', 'Lowest': 'text-gray-400' }[p] || 'text-gray-400');
      const statusDot = s => ({ 'To Do': 'bg-gray-400', 'In Progress': 'bg-blue-500', 'In Review': 'bg-yellow-400', 'Done': 'bg-emerald-500' }[s] || 'bg-gray-300');

      function renderIssues() {
        const start = (page - 1) * perPage;
        const slice = issues.slice(start, start + perPage);

        const countEl = document.getElementById('issues-count');
        if (countEl) countEl.textContent = (start + 1) + '–' + Math.min(start + perPage, issues.length) + ' of ' + issues.length;

        const listEl = document.getElementById('issues-list');
        if (!listEl) return;
        listEl.innerHTML = slice.map(function(issue) {
          const prio = (issue.fields.priority && issue.fields.priority.name) || '';
          const type = (issue.fields.issuetype && issue.fields.issuetype.name) || '';
          const typeHtml = type ? '<span class="text-xs text-gray-400 dark:text-gray-500">' + esc(type) + '</span>' : '';
          const assigneeHtml = issue.fields.assignee
            ? '<span class="text-xs text-gray-400 dark:text-gray-500">&middot; ' + esc(issue.fields.assignee.displayName) + '</span>'
            : '<span class="text-xs text-gray-300 dark:text-gray-600">&middot; Unassigned</span>';
          const prioHtml = prio ? '<span class="text-xs font-medium ' + priorityColor(prio) + ' ml-auto">' + esc(prio) + '</span>' : '';
          return '<div class="flex items-start gap-2.5 p-2.5 rounded-lg bg-gray-50 dark:bg-gray-700 hover:bg-gray-100 dark:hover:bg-gray-600 transition-colors">'
            + '<span class="w-2 h-2 rounded-full mt-1.5 flex-shrink-0 ' + statusDot(issue.fields.status.name) + '"></span>'
            + '<div class="flex-1 min-w-0">'
            + '<div class="flex items-center gap-1.5 mb-0.5"><span class="text-xs font-mono font-semibold text-blue-600">' + esc(issue.key) + '</span>' + typeHtml + '</div>'
            + '<p class="text-sm text-gray-800 dark:text-gray-200 truncate">' + esc(issue.fields.summary) + '</p>'
            + '<div class="flex items-center gap-2 mt-1"><span class="text-xs text-gray-500 dark:text-gray-400">' + esc(issue.fields.status.name) + '</span>' + assigneeHtml + prioHtml + '</div>'
            + '</div></div>';
        }).join('');

        renderIssuesPager();
      }

      function renderIssuesPager() {
        const el = document.getElementById('issues-pagination');
        if (!el) return;
        const tp = totalPages();
        if (tp <= 1) { el.innerHTML = ''; return; }

        const btnBase = 'px-2.5 py-1 text-xs rounded-lg border transition-colors';
        const btnActive = 'bg-blue-500 border-blue-500 text-white font-semibold';
        const btnInactive = 'bg-white dark:bg-gray-800 border-gray-200 dark:border-gray-700 text-gray-600 dark:text-gray-300 hover:border-blue-400 hover:text-blue-600';
        const btnDisabled = 'bg-white dark:bg-gray-800 border-gray-100 dark:border-gray-700 text-gray-300 dark:text-gray-600 cursor-not-allowed';

        let h = '<button onclick="issuesGoPage(' + (page - 1) + ')" ' + (page === 1 ? 'disabled' : '') + ' class="' + btnBase + ' ' + (page === 1 ? btnDisabled : btnInactive) + '">‹</button>';
        const pages = new Set([1, tp]);
        for (let p = Math.max(1, page - 2); p <= Math.min(tp, page + 2); p++) pages.add(p);
        let prev = null;
        for (const p of [...pages].sort((a, b) => a - b)) {
          if (prev !== null && p - prev > 1) h += '<span class="px-1 text-xs text-gray-400">…</span>';
          h += '<button onclick="issuesGoPage(' + p + ')" class="' + btnBase + ' ' + (p === page ? btnActive : btnInactive) + '">' + p + '</button>';
          prev = p;
        }
        h += '<button onclick="issuesGoPage(' + (page + 1) + ')" ' + (page === tp ? 'disabled' : '') + ' class="' + btnBase + ' ' + (page === tp ? btnDisabled : btnInactive) + '">›</button>';
        el.innerHTML = h;
      }

      window.issuesGoPage = function(p) {
        const tp = totalPages();
        if (p < 1 || p > tp) return;
        page = p;
        renderIssues();
        document.getElementById('issues-section').scrollIntoView({ behavior: 'smooth', block: 'start' });
      };

      renderIssues();
    }

    function showPillTooltip(event) {
      const assignees = JSON.parse(event.currentTarget.dataset.assignees || '{}');
      const entries = Object.entries(assignees).sort(function(a, b) { return b[1] - a[1]; });
      if (!entries.length) return;
      const el = document.getElementById('pill-tooltip');
      document.getElementById('pill-tooltip-inner').innerHTML = entries.map(function(e) {
        return '<div class="flex items-center justify-between gap-4">'
          + '<span class="flex items-center gap-1.5"><span class="w-1.5 h-1.5 rounded-full bg-amber-400 flex-shrink-0"></span>' + esc(e[0]) + '</span>'
          + '<span class="font-semibold text-amber-300">' + e[1] + '</span>'
          + '</div>';
      }).join('');
      el.classList.remove('hidden');
      const rect = event.currentTarget.getBoundingClientRect();
      const left = Math.min(rect.left, window.innerWidth - el.offsetWidth - 8);
      el.style.left = left + 'px';
      el.style.top = (rect.bottom + 6) + 'px';
    }

    function hidePillTooltip() {
      document.getElementById('pill-tooltip').classList.add('hidden');
    }

    function toggleTheme() {
      const isDark = document.documentElement.classList.toggle('dark');
      localStorage.setItem('theme', isDark ? 'dark' : 'light');
    }
  </script>

  <div id="pill-tooltip" class="fixed z-[200] hidden bg-gray-900 text-white text-xs rounded-lg px-3 py-2 shadow-xl pointer-events-none space-y-1">
    <div id="pill-tooltip-inner"></div>
  </div>

</body>
</html>`

type issueNode struct {
	Issue    pkgjira.Issue
	Children []pkgjira.Issue
}

func (h *Jira) RemainingView(ctx *fiber.Ctx) error {
	type viewData struct {
		Board       pkgjira.Board
		Sprint      *pkgjira.Sprint
		SprintStats pkgjira.SprintStats
		Nodes       []issueNode
		Total       int
		TotalSprint int
		HasMore     bool
		Error       string
	}

	data := viewData{}

	if h.client == nil {
		data.Error = "Jira integration is not configured."
	} else {
		boardID, err := strconv.Atoi(ctx.Params("id"))
		if err != nil {
			data.Error = "Invalid board ID."
		} else {
			summary, err := h.client.GetBoardSummary(ctx.Context(), boardID)
			if err != nil {
				data.Error = "Failed to fetch board: " + err.Error()
			} else {
				data.Board = summary.Board
				data.Sprint = summary.ActiveSprint
				data.SprintStats = summary.SprintStats
				data.TotalSprint = summary.TotalIssues
				data.HasMore = summary.TotalIssues > len(summary.Issues)

				// collect undone issues
				var remaining []pkgjira.Issue
				for _, issue := range summary.Issues {
					if issue.Fields.Status.StatusCategory.Key != "done" {
						remaining = append(remaining, issue)
					}
				}
				data.Total = len(remaining)

				// all issues are top-level; attach matching undone children beneath parents
				issueMap := make(map[string]pkgjira.Issue, len(remaining))
				for _, issue := range remaining {
					issueMap[issue.Key] = issue
				}
				for _, issue := range remaining {
					node := issueNode{Issue: issue}
					for _, sub := range issue.Fields.Subtasks {
						if child, ok := issueMap[sub.Key]; ok {
							node.Children = append(node.Children, child)
						}
					}
					data.Nodes = append(data.Nodes, node)
				}
			}
		}
	}

	tmpl, err := template.New("remaining").Funcs(template.FuncMap{
		"pct": func(done, total int) int {
			if total == 0 {
				return 0
			}
			return int(math.Round(float64(done) / float64(total) * 100))
		},
		"dateShort": func(s string) string {
			if len(s) >= 10 {
				return s[:10]
			}
			return s
		},
		"priorityClass": func(p string) string {
			m := map[string]string{
				"Highest": "text-red-600",
				"High":    "text-orange-500",
				"Medium":  "text-yellow-500",
				"Low":     "text-blue-400",
				"Lowest":  "text-gray-400",
			}
			if c, ok := m[p]; ok {
				return c
			}
			return "text-gray-400"
		},
		"statusDotClass": func(s string) string {
			m := map[string]string{
				"To Do":       "bg-gray-400",
				"In Progress": "bg-blue-500",
				"In Review":   "bg-yellow-400",
				"Done":        "bg-emerald-500",
			}
			if c, ok := m[s]; ok {
				return c
			}
			return "bg-gray-300"
		},
		"typeBadgeClass": func(t string) string {
			m := map[string]string{
				"Bug":      "bg-red-50 text-red-600 border-red-200",
				"Story":    "bg-purple-50 text-purple-600 border-purple-200",
				"Task":     "bg-blue-50 text-blue-600 border-blue-200",
				"Epic":     "bg-orange-50 text-orange-600 border-orange-200",
				"Sub-task": "bg-gray-50 text-gray-600 border-gray-200",
			}
			if c, ok := m[t]; ok {
				return c
			}
			return "bg-gray-50 text-gray-500 border-gray-200"
		},
		"spStr": func(f *float64) string {
			if f == nil || *f == 0 {
				return ""
			}
			v := *f
			if v == float64(int64(v)) {
				return strconv.Itoa(int(v)) + " SP"
			}
			return strconv.FormatFloat(v, 'f', 1, 64) + " SP"
		},
		"spNum": func(f *float64) string {
			if f == nil {
				return "0"
			}
			return strconv.FormatFloat(*f, 'f', 2, 64)
		},
	}).Parse(remainingTemplate)
	if err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString("template error: " + err.Error())
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString("render error: " + err.Error())
	}

	ctx.Set("Content-Type", "text/html; charset=utf-8")
	return ctx.Send(buf.Bytes())
}

func (h *Jira) IssuesView(ctx *fiber.Ctx) error {
	type viewData struct {
		Board       pkgjira.Board
		Sprint      *pkgjira.Sprint
		SprintStats pkgjira.SprintStats
		Nodes       []issueNode
		Total       int
		Done        int
		Remaining   int
		TotalSprint int
		HasMore     bool
		Error       string
	}

	data := viewData{}

	if h.client == nil {
		data.Error = "Jira integration is not configured."
	} else {
		boardID, err := strconv.Atoi(ctx.Params("id"))
		if err != nil {
			data.Error = "Invalid board ID."
		} else {
			summary, err := h.client.GetBoardSummary(ctx.Context(), boardID)
			if err != nil {
				data.Error = "Failed to fetch board: " + err.Error()
			} else {
				data.Board = summary.Board
				data.Sprint = summary.ActiveSprint
				data.SprintStats = summary.SprintStats
				data.TotalSprint = summary.TotalIssues
				data.HasMore = summary.TotalIssues > len(summary.Issues)
				data.Total = len(summary.Issues)

				for _, issue := range summary.Issues {
					if issue.Fields.Status.StatusCategory.Key == "done" {
						data.Done++
					}
				}
				data.Remaining = data.Total - data.Done

				issueMap := make(map[string]pkgjira.Issue, len(summary.Issues))
				for _, issue := range summary.Issues {
					issueMap[issue.Key] = issue
				}
				for _, issue := range summary.Issues {
					node := issueNode{Issue: issue}
					for _, sub := range issue.Fields.Subtasks {
						if child, ok := issueMap[sub.Key]; ok {
							node.Children = append(node.Children, child)
						}
					}
					data.Nodes = append(data.Nodes, node)
				}
			}
		}
	}

	tmpl, err := template.New("issues").Funcs(template.FuncMap{
		"pct": func(done, total int) int {
			if total == 0 {
				return 0
			}
			return int(math.Round(float64(done) / float64(total) * 100))
		},
		"dateShort": func(s string) string {
			if len(s) >= 10 {
				return s[:10]
			}
			return s
		},
		"priorityClass": func(p string) string {
			m := map[string]string{
				"Highest": "text-red-600",
				"High":    "text-orange-500",
				"Medium":  "text-yellow-500",
				"Low":     "text-blue-400",
				"Lowest":  "text-gray-400",
			}
			if c, ok := m[p]; ok {
				return c
			}
			return "text-gray-400"
		},
		"statusDotClass": func(s string) string {
			m := map[string]string{
				"To Do":       "bg-gray-400",
				"In Progress": "bg-blue-500",
				"In Review":   "bg-yellow-400",
				"Done":        "bg-emerald-500",
			}
			if c, ok := m[s]; ok {
				return c
			}
			return "bg-gray-300"
		},
		"typeBadgeClass": func(t string) string {
			m := map[string]string{
				"Bug":      "bg-red-50 text-red-600 border-red-200",
				"Story":    "bg-purple-50 text-purple-600 border-purple-200",
				"Task":     "bg-blue-50 text-blue-600 border-blue-200",
				"Epic":     "bg-orange-50 text-orange-600 border-orange-200",
				"Sub-task": "bg-gray-50 text-gray-600 border-gray-200",
			}
			if c, ok := m[t]; ok {
				return c
			}
			return "bg-gray-50 text-gray-500 border-gray-200"
		},
		"isDone": func(key string) bool {
			return key == "done"
		},
		"spStr": func(f *float64) string {
			if f == nil || *f == 0 {
				return ""
			}
			v := *f
			if v == float64(int64(v)) {
				return strconv.Itoa(int(v)) + " SP"
			}
			return strconv.FormatFloat(v, 'f', 1, 64) + " SP"
		},
		"spNum": func(f *float64) string {
			if f == nil {
				return "0"
			}
			return strconv.FormatFloat(*f, 'f', 2, 64)
		},
	}).Parse(issuesTemplate)
	if err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString("template error: " + err.Error())
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return ctx.Status(fiber.StatusInternalServerError).SendString("render error: " + err.Error())
	}

	ctx.Set("Content-Type", "text/html; charset=utf-8")
	return ctx.Send(buf.Bytes())
}

var issuesTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>All Issues{{if .Board.Name}} — {{.Board.Name}}{{end}}</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <script>tailwind.config={darkMode:'class'}</script>
  <script>(function(){var t=localStorage.getItem('theme');if(t==='dark'||(!t&&window.matchMedia('(prefers-color-scheme:dark)').matches))document.documentElement.classList.add('dark');})()</script>
</head>
<body class="bg-gray-50 dark:bg-gray-900 min-h-screen">

<div class="max-w-6xl mx-auto px-6 py-8">

  <!-- Back nav -->
  <div class="flex items-center justify-between mb-6">
    <a href="/jira/boards" class="inline-flex items-center gap-1.5 text-sm text-gray-500 dark:text-gray-400 hover:text-blue-600 transition-colors group">
      <svg class="w-4 h-4 group-hover:-translate-x-0.5 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7"/>
      </svg>
      Back to Boards
    </a>
    <button onclick="toggleTheme()" class="p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors" title="Toggle dark mode">
      <svg class="w-4 h-4 text-gray-500 dark:text-gray-400 dark:hidden" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z"/></svg>
      <svg class="w-4 h-4 text-gray-500 dark:text-gray-400 hidden dark:block" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364-6.364l-.707.707M6.343 17.657l-.707.707M17.657 17.657l-.707-.707M6.343 6.343l-.707-.707M16 12a4 4 0 11-8 0 4 4 0 018 0z"/></svg>
    </button>
  </div>

  {{if .Error}}
  <div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-4 text-sm text-red-700 dark:text-red-400 mb-6">{{.Error}}</div>
  {{else}}

  <!-- Header -->
  <div class="flex items-start justify-between gap-4 mb-6">
    <div>
      <div class="flex items-center gap-2 mb-1">
        {{if eq .Board.Type "scrum"}}
        <span class="text-xs font-medium bg-blue-100 text-blue-700 px-2 py-0.5 rounded-full">Scrum</span>
        {{else if eq .Board.Type "kanban"}}
        <span class="text-xs font-medium bg-green-100 text-green-700 px-2 py-0.5 rounded-full">Kanban</span>
        {{end}}
        <span class="text-xs text-gray-400 dark:text-gray-500">Board {{.Board.ID}}</span>
      </div>
      <h1 class="text-2xl font-bold text-gray-900 dark:text-gray-100">{{.Board.Name}}</h1>
      <p class="text-sm text-gray-500 dark:text-gray-400 mt-0.5">All issues</p>
    </div>
    <div class="text-right flex-shrink-0 space-y-1">
      <div>
        <p class="text-3xl font-bold text-blue-600">{{.Total}}</p>
        <p class="text-sm text-gray-400 dark:text-gray-500">total tickets</p>
      </div>
      <div class="flex items-center justify-end gap-3 text-xs text-gray-500 dark:text-gray-400">
        <span class="flex items-center gap-1"><span class="w-2 h-2 rounded-full bg-emerald-500"></span>{{.Done}} done</span>
        <span class="flex items-center gap-1"><span class="w-2 h-2 rounded-full bg-amber-400"></span>{{.Remaining}} remaining</span>
      </div>
    </div>
  </div>

  <!-- Sprint card -->
  {{if .Sprint}}
  {{$done  := .SprintStats.Done}}
  {{$total := .SprintStats.Total}}
  <div class="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-xl p-5 mb-6 shadow-sm">
    <div class="flex items-start justify-between gap-4 mb-3">
      <div>
        <p class="text-xs font-semibold text-blue-500 uppercase tracking-wide mb-0.5">{{if eq .Sprint.State "future"}}Future Sprint{{else}}Active Sprint{{end}}</p>
        <p class="text-base font-semibold text-gray-800 dark:text-gray-200">{{.Sprint.Name}}</p>
        {{if .Sprint.Goal}}<p class="text-sm text-gray-500 dark:text-gray-400 italic mt-0.5">&ldquo;{{.Sprint.Goal}}&rdquo;</p>{{end}}
      </div>
      {{if gt $total 0}}
      <div class="text-right flex-shrink-0">
        <p class="text-2xl font-bold text-gray-800 dark:text-gray-200">{{pct $done $total}}<span class="text-base font-normal text-gray-400 dark:text-gray-500">%</span></p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{$done}}/{{$total}} done</p>
      </div>
      {{end}}
    </div>
    {{if gt $total 0}}
    <div class="w-full h-2 bg-gray-100 dark:bg-gray-700 rounded-full overflow-hidden">
      <div class="h-full rounded-full bg-blue-500" style="width:{{pct $done $total}}%"></div>
    </div>
    {{end}}
    {{if or .Sprint.StartDate .Sprint.EndDate}}
    <div class="flex gap-4 text-xs text-gray-400 dark:text-gray-500 mt-2">
      {{if .Sprint.StartDate}}<span>Start: {{dateShort .Sprint.StartDate}}</span>{{end}}
      {{if .Sprint.EndDate}}<span>End: {{dateShort .Sprint.EndDate}}</span>{{end}}
    </div>
    {{end}}
  </div>
  {{end}}

  {{if .HasMore}}
  <div class="bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 rounded-lg px-4 py-3 text-sm text-amber-700 dark:text-amber-400 mb-4">
    Sprint has {{.TotalSprint}} issues total — showing the first 1000 fetched.
  </div>
  {{end}}

  {{if .Nodes}}
  <!-- Filter bar -->
  <div class="flex flex-col sm:flex-row gap-3 mb-3">
    <div class="relative flex-1">
      <svg class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400 pointer-events-none" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-4.35-4.35M17 11A6 6 0 1 1 5 11a6 6 0 0 1 12 0z"/>
      </svg>
      <input id="search-input" type="text" placeholder="Search by key or summary..."
             oninput="applyFilters()"
             class="w-full pl-10 pr-4 py-2.5 text-sm bg-white dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 border border-gray-200 rounded-xl shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500 placeholder-gray-400 dark:placeholder-gray-500" />
    </div>
    <select id="filter-status"   onchange="applyFilters()" class="text-sm bg-white dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 border border-gray-200 rounded-xl px-3 py-2.5 shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
      <option value="">All Statuses</option>
    </select>
    <select id="filter-type"     onchange="applyFilters()" class="text-sm bg-white dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 border border-gray-200 rounded-xl px-3 py-2.5 shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
      <option value="">All Types</option>
    </select>
    <select id="filter-assignee" onchange="applyFilters()" class="text-sm bg-white dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 border border-gray-200 rounded-xl px-3 py-2.5 shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
      <option value="">All Assignees</option>
    </select>
    <select id="filter-completion" onchange="applyFilters()" class="text-sm bg-white dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 border border-gray-200 rounded-xl px-3 py-2.5 shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
      <option value="">All</option>
      <option value="remaining">Remaining</option>
      <option value="done">Done</option>
    </select>
  </div>
  <div class="flex items-center gap-4 mb-3">
    <p id="ticket-count" class="text-sm text-gray-500 dark:text-gray-400">{{.Total}} ticket{{if gt .Total 1}}s{{end}} total</p>
    <span id="sp-count" class="text-sm font-semibold text-purple-600 dark:text-purple-400"></span>
  </div>

  <!-- Ticket list -->
  <div id="ticket-list" class="space-y-1.5">
    {{range .Nodes}}
    <div data-node
         data-status="{{.Issue.Fields.Status.Name}}"
         data-type="{{.Issue.Fields.IssueType.Name}}"
         data-assignee="{{if .Issue.Fields.Assignee}}{{.Issue.Fields.Assignee.DisplayName}}{{else}}Unassigned{{end}}"
         data-key="{{.Issue.Key}}"
         data-summary="{{.Issue.Fields.Summary}}"
         data-done="{{if isDone .Issue.Fields.Status.StatusCategory.Key}}done{{else}}remaining{{end}}"
         data-sp="{{spNum .Issue.Fields.StoryPoints}}">

      <!-- Parent row -->
      <div class="flex items-center gap-3 px-4 py-3 bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 hover:border-blue-200 dark:hover:border-blue-500 hover:shadow-sm transition-all {{if .Children}}cursor-pointer{{end}} {{if isDone .Issue.Fields.Status.StatusCategory.Key}}opacity-60{{end}}"
           {{if .Children}}onclick="toggleChildren('{{.Issue.Key}}')"{{end}}>
        {{if .Children}}
        <svg id="chevron-{{.Issue.Key}}" class="w-4 h-4 text-gray-400 dark:text-gray-500 flex-shrink-0 transition-transform duration-150" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/>
        </svg>
        {{else}}
        <span class="w-2.5 h-2.5 rounded-full flex-shrink-0 {{statusDotClass .Issue.Fields.Status.Name}}"></span>
        {{end}}
        <span class="text-xs font-mono font-semibold text-blue-600 w-28 flex-shrink-0">{{.Issue.Key}}</span>
        {{if .Issue.Fields.IssueType.Name}}
        <span class="text-xs font-medium px-2 py-0.5 rounded-full border flex-shrink-0 {{typeBadgeClass .Issue.Fields.IssueType.Name}}">{{.Issue.Fields.IssueType.Name}}</span>
        {{end}}
        <span class="text-sm text-gray-800 dark:text-gray-200 flex-1 min-w-0 {{if isDone .Issue.Fields.Status.StatusCategory.Key}}line-through text-gray-400{{end}}">{{.Issue.Fields.Summary}}</span>
        {{if .Children}}
        <span class="text-xs text-gray-400 dark:text-gray-500 flex-shrink-0 bg-gray-100 dark:bg-gray-700 px-2 py-0.5 rounded-full">{{len .Children}} sub-task{{if gt (len .Children) 1}}s{{end}}</span>
        {{end}}
        <span class="text-xs text-gray-500 dark:text-gray-400 flex-shrink-0 w-36 text-right truncate">
          {{if .Issue.Fields.Assignee}}{{.Issue.Fields.Assignee.DisplayName}}{{else}}<span class="text-gray-300 dark:text-gray-600">Unassigned</span>{{end}}
        </span>
        {{if .Issue.Fields.Priority}}
        <span class="text-xs font-medium flex-shrink-0 w-16 text-right {{priorityClass .Issue.Fields.Priority.Name}}">{{.Issue.Fields.Priority.Name}}</span>
        {{end}}
        <span class="text-xs text-gray-400 dark:text-gray-500 flex-shrink-0 w-28 text-right">{{.Issue.Fields.Status.Name}}</span>
        <span class="text-xs font-semibold flex-shrink-0 w-14 text-right {{if .Issue.Fields.StoryPoints}}text-purple-600 dark:text-purple-400{{else}}text-gray-300 dark:text-gray-600{{end}}">{{if .Issue.Fields.StoryPoints}}{{spStr .Issue.Fields.StoryPoints}}{{else}}—{{end}}</span>
      </div>

      <!-- Children (hidden by default) -->
      {{if .Children}}
      <div id="children-{{.Issue.Key}}" class="hidden ml-8 mt-1 space-y-1">
        {{range .Children}}
        <div class="flex items-center gap-3 px-4 py-2.5 bg-gray-50 dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 hover:bg-gray-100 dark:hover:bg-gray-700 transition-all {{if isDone .Fields.Status.StatusCategory.Key}}opacity-60{{end}}"
             data-child
             data-status="{{.Fields.Status.Name}}"
             data-type="{{.Fields.IssueType.Name}}"
             data-assignee="{{if .Fields.Assignee}}{{.Fields.Assignee.DisplayName}}{{else}}Unassigned{{end}}"
             data-key="{{.Key}}"
             data-summary="{{.Fields.Summary}}"
             data-sp="{{spNum .Fields.StoryPoints}}">
          <span class="w-2 h-2 rounded-full flex-shrink-0 {{statusDotClass .Fields.Status.Name}}"></span>
          <span class="text-xs font-mono font-semibold text-blue-500 w-28 flex-shrink-0">{{.Key}}</span>
          {{if .Fields.IssueType.Name}}
          <span class="text-xs font-medium px-2 py-0.5 rounded-full border flex-shrink-0 {{typeBadgeClass .Fields.IssueType.Name}}">{{.Fields.IssueType.Name}}</span>
          {{end}}
          <span class="text-sm text-gray-700 dark:text-gray-300 flex-1 min-w-0 {{if isDone .Fields.Status.StatusCategory.Key}}line-through text-gray-400{{end}}">{{.Fields.Summary}}</span>
          <span class="text-xs text-gray-400 dark:text-gray-500 flex-shrink-0 w-36 text-right truncate">
            {{if .Fields.Assignee}}{{.Fields.Assignee.DisplayName}}{{else}}<span class="text-gray-300 dark:text-gray-600">Unassigned</span>{{end}}
          </span>
          {{if .Fields.Priority}}
          <span class="text-xs font-medium flex-shrink-0 w-16 text-right {{priorityClass .Fields.Priority.Name}}">{{.Fields.Priority.Name}}</span>
          {{end}}
          <span class="text-xs text-gray-400 dark:text-gray-500 flex-shrink-0 w-28 text-right">{{.Fields.Status.Name}}</span>
          <span class="text-xs font-semibold flex-shrink-0 w-14 text-right {{if .Fields.StoryPoints}}text-purple-600 dark:text-purple-400{{else}}text-gray-300 dark:text-gray-600{{end}}">{{if .Fields.StoryPoints}}{{spStr .Fields.StoryPoints}}{{else}}—{{end}}</span>
        </div>
        {{end}}
      </div>
      {{end}}

    </div>
    {{end}}
  </div>

  {{else}}
  <div class="text-center py-24 text-gray-400 dark:text-gray-500">
    <svg class="w-14 h-14 mx-auto mb-4 opacity-40" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M9 17V7m0 10a2 2 0 01-2 2H5a2 2 0 01-2-2V7a2 2 0 012-2h2a2 2 0 012 2m0 10a2 2 0 002 2h2a2 2 0 002-2M9 7a2 2 0 012-2h2a2 2 0 012 2m0 10V7"/>
    </svg>
    <p class="text-sm font-medium">No issues found</p>
    <p class="text-xs mt-1">This board has no issues in the current sprint.</p>
  </div>
  {{end}}

  {{end}}
</div>

<script>
  function toggleTheme() {
    const isDark = document.documentElement.classList.toggle('dark');
    localStorage.setItem('theme', isDark ? 'dark' : 'light');
  }

  (function() {
    const statuses = new Set(), types = new Set(), assignees = new Set();
    document.querySelectorAll('#ticket-list [data-node]').forEach(function(r) {
      if (r.dataset.status)   statuses.add(r.dataset.status);
      if (r.dataset.type)     types.add(r.dataset.type);
      if (r.dataset.assignee) assignees.add(r.dataset.assignee);
    });
    function populate(id, values) {
      const sel = document.getElementById(id);
      if (!sel) return;
      [...values].sort().forEach(function(v) {
        const opt = document.createElement('option');
        opt.value = v; opt.textContent = v;
        sel.appendChild(opt);
      });
    }
    populate('filter-status', statuses);
    populate('filter-type', types);
    populate('filter-assignee', assignees);

    // initialise SP total
    let initSP = 0;
    document.querySelectorAll('#ticket-list [data-node]').forEach(function(n) {
      initSP += parseFloat(n.dataset.sp || 0);
    });
    const spEl = document.getElementById('sp-count');
    if (spEl && initSP > 0) {
      const r = Math.round(initSP * 10) / 10;
      spEl.textContent = r + ' SP total';
    }
  })();

  function toggleChildren(key) {
    const children = document.getElementById('children-' + key);
    const chevron  = document.getElementById('chevron-' + key);
    const open = children.classList.toggle('hidden') === false;
    if (chevron) chevron.style.transform = open ? 'rotate(90deg)' : '';
  }

  function applyFilters() {
    const q          = (document.getElementById('search-input').value || '').toLowerCase().trim();
    const status     = document.getElementById('filter-status').value;
    const type       = document.getElementById('filter-type').value;
    const assignee   = document.getElementById('filter-assignee').value;
    const completion = document.getElementById('filter-completion').value;
    const nodes = document.querySelectorAll('#ticket-list [data-node]');
    let visible = 0;
    let totalSP = 0;
    nodes.forEach(function(node) {
      const matchSearch     = !q          || (node.dataset.key + ' ' + node.dataset.summary).toLowerCase().includes(q);
      const matchStatus     = !status     || node.dataset.status   === status;
      const matchType       = !type       || node.dataset.type     === type;
      const matchAssignee   = !assignee   || node.dataset.assignee === assignee;
      const matchCompletion = !completion || node.dataset.done     === completion;
      const show = matchSearch && matchStatus && matchType && matchAssignee && matchCompletion;
      node.style.display = show ? '' : 'none';
      if (show) {
        visible++;
        totalSP += parseFloat(node.dataset.sp || 0);
      }
    });
    const el = document.getElementById('ticket-count');
    if (el) el.textContent = visible + ' ticket' + (visible !== 1 ? 's' : '') + ' shown';
    const spEl = document.getElementById('sp-count');
    if (spEl) {
      const r = Math.round(totalSP * 10) / 10;
      spEl.textContent = r > 0 ? r + ' SP' : '';
    }
  }
</script>

</body>
</html>`

var remainingTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Remaining Work{{if .Board.Name}} — {{.Board.Name}}{{end}}</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <script>tailwind.config={darkMode:'class'}</script>
  <script>(function(){var t=localStorage.getItem('theme');if(t==='dark'||(!t&&window.matchMedia('(prefers-color-scheme:dark)').matches))document.documentElement.classList.add('dark');})()</script>
</head>
<body class="bg-gray-50 dark:bg-gray-900 min-h-screen">

<div class="max-w-6xl mx-auto px-6 py-8">

  <!-- Back nav -->
  <div class="flex items-center justify-between mb-6">
    <a href="/jira/boards" class="inline-flex items-center gap-1.5 text-sm text-gray-500 dark:text-gray-400 hover:text-blue-600 transition-colors group">
      <svg class="w-4 h-4 group-hover:-translate-x-0.5 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7"/>
      </svg>
      Back to Boards
    </a>
    <button onclick="toggleTheme()" class="p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors" title="Toggle dark mode">
      <svg class="w-4 h-4 text-gray-500 dark:text-gray-400 dark:hidden" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z"/></svg>
      <svg class="w-4 h-4 text-gray-500 dark:text-gray-400 hidden dark:block" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364-6.364l-.707.707M6.343 17.657l-.707.707M17.657 17.657l-.707-.707M6.343 6.343l-.707-.707M16 12a4 4 0 11-8 0 4 4 0 018 0z"/></svg>
    </button>
  </div>

  <!-- Claude Runner Modal -->
  <div id="claude-modal" class="hidden fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-sm">
    <div class="bg-white dark:bg-gray-800 rounded-2xl shadow-2xl w-full max-w-2xl border border-gray-200 dark:border-gray-700">
      <div class="flex items-center justify-between px-6 py-4 border-b border-gray-100 dark:border-gray-700">
        <div class="flex items-center gap-2">
          <svg class="w-5 h-5 text-violet-600" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 3l14 9-14 9V3z"/></svg>
          <div>
            <h2 class="text-base font-semibold text-gray-900 dark:text-gray-100">Run Claude</h2>
            <p id="claude-ticket-label" class="text-xs font-mono text-violet-600 dark:text-violet-400 mt-0.5"></p>
          </div>
        </div>
        <button onclick="closeClaudeModal()" class="p-1.5 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 text-gray-400 transition-colors">
          <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/></svg>
        </button>
      </div>
      <div class="px-6 py-4 space-y-3 max-h-[70vh] overflow-y-auto">
        <!-- Loading skeleton -->
        <div id="claude-loading" class="space-y-2">
          <div class="h-4 bg-gray-200 dark:bg-gray-700 rounded animate-pulse w-3/4"></div>
          <div class="h-16 bg-gray-200 dark:bg-gray-700 rounded animate-pulse"></div>
          <div class="h-4 bg-gray-200 dark:bg-gray-700 rounded animate-pulse w-1/2"></div>
        </div>
        <!-- Ticket detail fields (disabled) -->
        <div id="claude-ticket-fields" class="hidden space-y-3">
          <div>
            <label class="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Summary</label>
            <input id="claude-field-summary" disabled type="text"
              class="w-full px-3 py-2 text-sm bg-gray-100 dark:bg-gray-900 border border-gray-200 dark:border-gray-700 text-gray-700 dark:text-gray-300 rounded-lg cursor-not-allowed" />
          </div>
          <div class="grid grid-cols-4 gap-2">
            <div>
              <label class="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Type</label>
              <input id="claude-field-type" disabled type="text"
                class="w-full px-3 py-2 text-sm bg-gray-100 dark:bg-gray-900 border border-gray-200 dark:border-gray-700 text-gray-700 dark:text-gray-300 rounded-lg cursor-not-allowed" />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Priority</label>
              <input id="claude-field-priority" disabled type="text"
                class="w-full px-3 py-2 text-sm bg-gray-100 dark:bg-gray-900 border border-gray-200 dark:border-gray-700 text-gray-700 dark:text-gray-300 rounded-lg cursor-not-allowed" />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Story Points</label>
              <input id="claude-field-sp" disabled type="text"
                class="w-full px-3 py-2 text-sm bg-gray-100 dark:bg-gray-900 border border-gray-200 dark:border-gray-700 text-gray-700 dark:text-gray-300 rounded-lg cursor-not-allowed" />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Assignee</label>
              <input id="claude-field-assignee" disabled type="text"
                class="w-full px-3 py-2 text-sm bg-gray-100 dark:bg-gray-900 border border-gray-200 dark:border-gray-700 text-gray-700 dark:text-gray-300 rounded-lg cursor-not-allowed" />
            </div>
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">GitHub Repo</label>
            <input id="claude-field-repo" disabled type="text"
              class="w-full px-3 py-2 text-sm bg-gray-100 dark:bg-gray-900 border border-gray-200 dark:border-gray-700 text-gray-700 dark:text-gray-300 rounded-lg cursor-not-allowed" />
          </div>
          <div class="grid grid-cols-2 gap-2">
            <div>
              <label class="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Branch</label>
              <input id="claude-field-base" disabled type="text"
                class="w-full px-3 py-2 text-sm bg-gray-100 dark:bg-gray-900 border border-gray-200 dark:border-gray-700 text-gray-700 dark:text-gray-300 rounded-lg cursor-not-allowed" />
            </div>
            <div>
              <label class="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Feature Branch</label>
              <input id="claude-field-feature" type="text"
                class="w-full px-3 py-2 text-sm bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 text-gray-800 dark:text-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-violet-500" />
            </div>
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-500 dark:text-gray-400 mb-1">Prompts</label>
            <div id="claude-field-description"
              class="w-full px-3 py-2.5 text-sm bg-gray-100 dark:bg-gray-900 border border-gray-200 dark:border-gray-700 text-gray-700 dark:text-gray-300 rounded-lg max-h-52 overflow-y-auto
                     prose prose-sm dark:prose-invert max-w-none
                     prose-headings:font-semibold prose-headings:text-gray-800 dark:prose-headings:text-gray-200
                     prose-p:my-1 prose-ul:my-1 prose-ol:my-1 prose-li:my-0
                     prose-code:bg-gray-200 dark:prose-code:bg-gray-700 prose-code:px-1 prose-code:rounded"></div>
          </div>
        </div>
        <div id="claude-fetch-error" class="hidden rounded-xl border border-red-200 dark:border-red-800 bg-red-50 dark:bg-red-900/20 p-3 text-sm text-red-700 dark:text-red-400"></div>
        <div id="claude-inprogress" class="hidden rounded-xl border border-yellow-200 dark:border-yellow-800 bg-yellow-50 dark:bg-yellow-900/20 p-3 text-sm text-yellow-700 dark:text-yellow-400"></div>
        <div id="claude-result" class="hidden rounded-xl border border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-900 p-3 text-sm text-gray-800 dark:text-gray-200 max-h-48 overflow-y-auto whitespace-pre-wrap"></div>
        <div id="claude-error" class="hidden rounded-xl border border-red-200 dark:border-red-800 bg-red-50 dark:bg-red-900/20 p-3 text-sm text-red-700 dark:text-red-400"></div>
      </div>
      <div class="flex items-center justify-end gap-2 px-6 py-4 border-t border-gray-100 dark:border-gray-700">
        <button onclick="closeClaudeModal()" class="px-4 py-2 text-sm font-medium text-gray-600 dark:text-gray-400 hover:text-gray-800 dark:hover:text-gray-200 transition-colors">Cancel</button>
        <button id="claude-submit" onclick="submitClaudeRunner()" class="inline-flex items-center gap-1.5 px-4 py-2 text-sm font-medium bg-violet-600 hover:bg-violet-700 text-white rounded-lg transition-colors">
          <svg id="claude-spinner" class="hidden w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v4a4 4 0 00-4 4H4z"/></svg>
          <span id="claude-btn-label">Execute</span>
        </button>
      </div>
    </div>
  </div>

  {{if .Error}}
  <div class="bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg p-4 text-sm text-red-700 dark:text-red-400 mb-6">{{.Error}}</div>
  {{else}}

  <!-- Header -->
  <div class="flex items-start justify-between gap-4 mb-6">
    <div>
      <div class="flex items-center gap-2 mb-1">
        {{if eq .Board.Type "scrum"}}
        <span class="text-xs font-medium bg-blue-100 text-blue-700 px-2 py-0.5 rounded-full">Scrum</span>
        {{else if eq .Board.Type "kanban"}}
        <span class="text-xs font-medium bg-green-100 text-green-700 px-2 py-0.5 rounded-full">Kanban</span>
        {{end}}
        <span class="text-xs text-gray-400 dark:text-gray-500">Board {{.Board.ID}}</span>
      </div>
      <h1 class="text-2xl font-bold text-gray-900 dark:text-gray-100">{{.Board.Name}}</h1>
      <p class="text-sm text-gray-500 dark:text-gray-400 mt-0.5">Remaining work</p>
    </div>
    <div class="text-right flex-shrink-0">
      <p class="text-3xl font-bold text-amber-600">{{.Total}}</p>
      <p class="text-sm text-gray-400 dark:text-gray-500">remaining tickets</p>
    </div>
  </div>

  <!-- Sprint card -->
  {{if .Sprint}}
  {{$done  := .SprintStats.Done}}
  {{$total := .SprintStats.Total}}
  <div class="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-xl p-5 mb-6 shadow-sm">
    <div class="flex items-start justify-between gap-4 mb-3">
      <div>
        <p class="text-xs font-semibold text-blue-500 uppercase tracking-wide mb-0.5">{{if eq .Sprint.State "future"}}Future Sprint{{else}}Active Sprint{{end}}</p>
        <p class="text-base font-semibold text-gray-800 dark:text-gray-200">{{.Sprint.Name}}</p>
        {{if .Sprint.Goal}}<p class="text-sm text-gray-500 dark:text-gray-400 italic mt-0.5">&ldquo;{{.Sprint.Goal}}&rdquo;</p>{{end}}
      </div>
      {{if gt $total 0}}
      <div class="text-right flex-shrink-0">
        <p class="text-2xl font-bold text-gray-800 dark:text-gray-200">{{pct $done $total}}<span class="text-base font-normal text-gray-400 dark:text-gray-500">%</span></p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{$done}}/{{$total}} done</p>
      </div>
      {{end}}
    </div>
    {{if gt $total 0}}
    <div class="w-full h-2 bg-gray-100 dark:bg-gray-700 rounded-full overflow-hidden">
      <div class="h-full rounded-full bg-blue-500" style="width:{{pct $done $total}}%"></div>
    </div>
    {{end}}
    {{if or .Sprint.StartDate .Sprint.EndDate}}
    <div class="flex gap-4 text-xs text-gray-400 dark:text-gray-500 mt-2">
      {{if .Sprint.StartDate}}<span>Start: {{dateShort .Sprint.StartDate}}</span>{{end}}
      {{if .Sprint.EndDate}}<span>End: {{dateShort .Sprint.EndDate}}</span>{{end}}
    </div>
    {{end}}
  </div>
  {{end}}

  {{if .HasMore}}
  <div class="bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 rounded-lg px-4 py-3 text-sm text-amber-700 dark:text-amber-400 mb-4">
    Sprint has {{.TotalSprint}} issues total — showing undone from the first 200 fetched.
  </div>
  {{end}}

  {{if .Nodes}}
  <!-- Filter bar -->
  <div class="flex flex-col sm:flex-row gap-3 mb-3">
    <div class="relative flex-1">
      <svg class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400 pointer-events-none" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-4.35-4.35M17 11A6 6 0 1 1 5 11a6 6 0 0 1 12 0z"/>
      </svg>
      <input id="search-input" type="text" placeholder="Search by key or summary..."
             oninput="applyFilters()"
             class="w-full pl-10 pr-4 py-2.5 text-sm bg-white dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 border border-gray-200 rounded-xl shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500 placeholder-gray-400 dark:placeholder-gray-500" />
    </div>
    <select id="filter-status"   onchange="applyFilters()" class="text-sm bg-white dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 border border-gray-200 rounded-xl px-3 py-2.5 shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
      <option value="">All Statuses</option>
    </select>
    <select id="filter-type"     onchange="applyFilters()" class="text-sm bg-white dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 border border-gray-200 rounded-xl px-3 py-2.5 shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
      <option value="">All Types</option>
    </select>
    <select id="filter-assignee" onchange="applyFilters()" class="text-sm bg-white dark:bg-gray-800 dark:border-gray-700 dark:text-gray-200 border border-gray-200 rounded-xl px-3 py-2.5 shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
      <option value="">All Assignees</option>
    </select>
  </div>
  <div class="flex items-center gap-4 mb-3">
    <p id="ticket-count" class="text-sm text-gray-500 dark:text-gray-400">{{.Total}} ticket{{if gt .Total 1}}s{{end}} remaining</p>
    <span id="sp-count" class="text-sm font-semibold text-purple-600 dark:text-purple-400"></span>
  </div>

  <!-- Ticket list -->
  <div id="ticket-list" class="space-y-1.5">
    {{range .Nodes}}
    <div data-node
         data-status="{{.Issue.Fields.Status.Name}}"
         data-type="{{.Issue.Fields.IssueType.Name}}"
         data-assignee="{{if .Issue.Fields.Assignee}}{{.Issue.Fields.Assignee.DisplayName}}{{else}}Unassigned{{end}}"
         data-key="{{.Issue.Key}}"
         data-summary="{{.Issue.Fields.Summary}}"
         data-sp="{{spNum .Issue.Fields.StoryPoints}}">

      <!-- Parent row -->
      <div class="flex items-center gap-3 px-4 py-3 bg-white dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 hover:border-blue-200 dark:hover:border-blue-500 hover:shadow-sm transition-all {{if .Children}}cursor-pointer{{end}}"
           {{if .Children}}onclick="toggleChildren('{{.Issue.Key}}')"{{end}}>
        {{if .Children}}
        <svg id="chevron-{{.Issue.Key}}" class="w-4 h-4 text-gray-400 dark:text-gray-500 flex-shrink-0 transition-transform duration-150" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/>
        </svg>
        {{else}}
        <span class="w-2.5 h-2.5 rounded-full flex-shrink-0 {{statusDotClass .Issue.Fields.Status.Name}}"></span>
        {{end}}
        <span class="text-xs font-mono font-semibold text-blue-600 w-28 flex-shrink-0">{{.Issue.Key}}</span>
        {{if .Issue.Fields.IssueType.Name}}
        <span class="text-xs font-medium px-2 py-0.5 rounded-full border flex-shrink-0 {{typeBadgeClass .Issue.Fields.IssueType.Name}}">{{.Issue.Fields.IssueType.Name}}</span>
        {{end}}
        <span class="text-sm text-gray-800 dark:text-gray-200 flex-1 min-w-0">{{.Issue.Fields.Summary}}</span>
        {{if .Children}}
        <span class="text-xs text-gray-400 dark:text-gray-500 flex-shrink-0 bg-gray-100 dark:bg-gray-700 px-2 py-0.5 rounded-full">{{len .Children}} sub-task{{if gt (len .Children) 1}}s{{end}}</span>
        {{end}}
        <span class="text-xs text-gray-500 dark:text-gray-400 flex-shrink-0 w-36 text-right truncate">
          {{if .Issue.Fields.Assignee}}{{.Issue.Fields.Assignee.DisplayName}}{{else}}<span class="text-gray-300 dark:text-gray-600">Unassigned</span>{{end}}
        </span>
        {{if .Issue.Fields.Priority}}
        <span class="text-xs font-medium flex-shrink-0 w-16 text-right {{priorityClass .Issue.Fields.Priority.Name}}">{{.Issue.Fields.Priority.Name}}</span>
        {{end}}
        <span class="text-xs text-gray-400 dark:text-gray-500 flex-shrink-0 w-28 text-right">{{.Issue.Fields.Status.Name}}</span>
        <span class="text-xs font-semibold flex-shrink-0 w-14 text-right {{if .Issue.Fields.StoryPoints}}text-purple-600 dark:text-purple-400{{else}}text-gray-300 dark:text-gray-600{{end}}">{{if .Issue.Fields.StoryPoints}}{{spStr .Issue.Fields.StoryPoints}}{{else}}—{{end}}</span>
        {{if and (ne .Issue.Fields.IssueType.Name "Epic") (ne .Issue.Fields.IssueType.Name "Story") (eq .Issue.Fields.Status.Name "Awaiting for AI Agent")}}
        <button onclick="event.stopPropagation(); openClaudeModal(this)" data-key="{{.Issue.Key}}" data-summary="{{.Issue.Fields.Summary}}"
          class="flex-shrink-0 p-1.5 rounded-lg text-violet-500 hover:bg-violet-50 dark:hover:bg-violet-900/30 transition-colors" title="Run Claude on {{.Issue.Key}}">
          <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 3l14 9-14 9V3z"/></svg>
        </button>
        {{end}}
        <button onclick="event.stopPropagation(); viewTicketLogs('{{.Issue.Key}}')"
          class="flex-shrink-0 p-1.5 rounded-lg text-gray-400 hover:text-blue-500 hover:bg-blue-50 dark:hover:bg-blue-900/30 transition-colors" title="View execution logs for {{.Issue.Key}}">
          <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 10h16M4 14h10"/></svg>
        </button>
      </div>

      <!-- Children (hidden by default) -->
      {{if .Children}}
      <div id="children-{{.Issue.Key}}" class="hidden ml-8 mt-1 space-y-1">
        {{range .Children}}
        <div class="flex items-center gap-3 px-4 py-2.5 bg-gray-50 dark:bg-gray-800 rounded-xl border border-gray-100 dark:border-gray-700 hover:bg-gray-100 dark:hover:bg-gray-700 transition-all"
             data-child
             data-status="{{.Fields.Status.Name}}"
             data-type="{{.Fields.IssueType.Name}}"
             data-assignee="{{if .Fields.Assignee}}{{.Fields.Assignee.DisplayName}}{{else}}Unassigned{{end}}"
             data-key="{{.Key}}"
             data-summary="{{.Fields.Summary}}"
             data-sp="{{spNum .Fields.StoryPoints}}">
          <span class="w-2 h-2 rounded-full flex-shrink-0 {{statusDotClass .Fields.Status.Name}}"></span>
          <span class="text-xs font-mono font-semibold text-blue-500 w-28 flex-shrink-0">{{.Key}}</span>
          {{if .Fields.IssueType.Name}}
          <span class="text-xs font-medium px-2 py-0.5 rounded-full border flex-shrink-0 {{typeBadgeClass .Fields.IssueType.Name}}">{{.Fields.IssueType.Name}}</span>
          {{end}}
          <span class="text-sm text-gray-700 dark:text-gray-300 flex-1 min-w-0">{{.Fields.Summary}}</span>
          <span class="text-xs text-gray-400 dark:text-gray-500 flex-shrink-0 w-36 text-right truncate">
            {{if .Fields.Assignee}}{{.Fields.Assignee.DisplayName}}{{else}}<span class="text-gray-300 dark:text-gray-600">Unassigned</span>{{end}}
          </span>
          {{if .Fields.Priority}}
          <span class="text-xs font-medium flex-shrink-0 w-16 text-right {{priorityClass .Fields.Priority.Name}}">{{.Fields.Priority.Name}}</span>
          {{end}}
          <span class="text-xs text-gray-400 dark:text-gray-500 flex-shrink-0 w-28 text-right">{{.Fields.Status.Name}}</span>
          <span class="text-xs font-semibold flex-shrink-0 w-14 text-right {{if .Fields.StoryPoints}}text-purple-600 dark:text-purple-400{{else}}text-gray-300 dark:text-gray-600{{end}}">{{if .Fields.StoryPoints}}{{spStr .Fields.StoryPoints}}{{else}}—{{end}}</span>
          {{if and (ne .Fields.IssueType.Name "Epic") (ne .Fields.IssueType.Name "Story") (eq .Fields.Status.Name "Awaiting for AI Agent")}}
          <button onclick="event.stopPropagation(); openClaudeModal(this)" data-key="{{.Key}}" data-summary="{{.Fields.Summary}}"
            class="flex-shrink-0 p-1.5 rounded-lg text-violet-500 hover:bg-violet-50 dark:hover:bg-violet-900/30 transition-colors" title="Run Claude on {{.Key}}">
            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 3l14 9-14 9V3z"/></svg>
          </button>
          {{end}}
          <button onclick="event.stopPropagation(); viewTicketLogs('{{.Key}}')"
            class="flex-shrink-0 p-1.5 rounded-lg text-gray-400 hover:text-blue-500 hover:bg-blue-50 dark:hover:bg-blue-900/30 transition-colors" title="View execution logs for {{.Key}}">
            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 10h16M4 14h10"/></svg>
          </button>
        </div>
        {{end}}
      </div>
      {{end}}

    </div>
    {{end}}
  </div>

  {{else}}
  <div class="text-center py-24 text-gray-400 dark:text-gray-500">
    <svg class="w-14 h-14 mx-auto mb-4 opacity-40" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"/>
    </svg>
    <p class="text-sm font-medium">Everything is done!</p>
    <p class="text-xs mt-1">No remaining work in this sprint.</p>
  </div>
  {{end}}

  {{end}}
</div>

<script>
  function toggleTheme() {
    const isDark = document.documentElement.classList.toggle('dark');
    localStorage.setItem('theme', isDark ? 'dark' : 'light');
  }

  (function() {
    const statuses = new Set(), types = new Set(), assignees = new Set();
    document.querySelectorAll('#ticket-list [data-node]').forEach(function(r) {
      if (r.dataset.status)   statuses.add(r.dataset.status);
      if (r.dataset.type)     types.add(r.dataset.type);
      if (r.dataset.assignee) assignees.add(r.dataset.assignee);
    });
    function populate(id, values) {
      const sel = document.getElementById(id);
      if (!sel) return;
      [...values].sort().forEach(function(v) {
        const opt = document.createElement('option');
        opt.value = v; opt.textContent = v;
        sel.appendChild(opt);
      });
    }
    populate('filter-status', statuses);
    populate('filter-type', types);
    populate('filter-assignee', assignees);

    let initSP = 0;
    document.querySelectorAll('#ticket-list [data-node]').forEach(function(n) {
      initSP += parseFloat(n.dataset.sp || 0);
    });
    const spEl = document.getElementById('sp-count');
    if (spEl && initSP > 0) {
      const r = Math.round(initSP * 10) / 10;
      spEl.textContent = r + ' SP total';
    }
  })();

  function toggleChildren(key) {
    const children = document.getElementById('children-' + key);
    const chevron  = document.getElementById('chevron-' + key);
    const open = children.classList.toggle('hidden') === false;
    if (chevron) chevron.style.transform = open ? 'rotate(90deg)' : '';
  }

  function applyFilters() {
    const q        = (document.getElementById('search-input').value || '').toLowerCase().trim();
    const status   = document.getElementById('filter-status').value;
    const type     = document.getElementById('filter-type').value;
    const assignee = document.getElementById('filter-assignee').value;
    const nodes = document.querySelectorAll('#ticket-list [data-node]');
    let visible = 0;
    let totalSP = 0;
    nodes.forEach(function(node) {
      const matchSearch   = !q        || (node.dataset.key + ' ' + node.dataset.summary).toLowerCase().includes(q);
      const matchStatus   = !status   || node.dataset.status   === status;
      const matchType     = !type     || node.dataset.type     === type;
      const matchAssignee = !assignee || node.dataset.assignee === assignee;
      const show = matchSearch && matchStatus && matchType && matchAssignee;
      node.style.display = show ? '' : 'none';
      if (show) {
        visible++;
        totalSP += parseFloat(node.dataset.sp || 0);
      }
    });
    const el = document.getElementById('ticket-count');
    if (el) el.textContent = visible + ' ticket' + (visible !== 1 ? 's' : '') + ' remaining';
    const spEl = document.getElementById('sp-count');
    if (spEl) {
      const r = Math.round(totalSP * 10) / 10;
      spEl.textContent = r > 0 ? r + ' SP' : '';
    }
  }

  var _claudeBoardID = {{.Board.ID}};
  var _claudeTicketKey = '';
  var _claudeTicketDetail = {};

  function stripHtml(html) {
    var d = document.createElement('div');
    d.innerHTML = html;
    return (d.innerText || d.textContent || '').trim();
  }

  var _activeStatuses = ['pending', 'queued', 'cloning', 'running'];

  function openClaudeModal(btn) {
    _claudeTicketKey = btn.dataset.key || '';
    _claudeTicketDetail = {};

    document.getElementById('claude-ticket-label').textContent = _claudeTicketKey;
    document.getElementById('claude-result').classList.add('hidden');
    document.getElementById('claude-error').classList.add('hidden');
    document.getElementById('claude-fetch-error').classList.add('hidden');
    document.getElementById('claude-inprogress').classList.add('hidden');
    document.getElementById('claude-ticket-fields').classList.add('hidden');
    document.getElementById('claude-loading').classList.remove('hidden');
    document.getElementById('claude-submit').disabled = true;
    document.getElementById('claude-modal').classList.remove('hidden');

    Promise.all([
      fetch('/jira/ticket/' + _claudeTicketKey).then(function(r) { return r.json(); }),
      fetch('/jira/executions?ticketId=' + _claudeTicketKey).then(function(r) { return r.json(); }).catch(function() { return []; })
    ]).then(function(results) {
      var ticketRes  = results[0];
      var executions = Array.isArray(results[1]) ? results[1] : [];

      document.getElementById('claude-loading').classList.add('hidden');

      var inProgress = null;
      for (var i = 0; i < executions.length; i++) {
        if (_activeStatuses.indexOf(executions[i].status) !== -1) {
          inProgress = executions[i];
          break;
        }
      }

      if (inProgress) {
        var ipEl = document.getElementById('claude-inprogress');
        ipEl.innerHTML = 'An execution is already in progress (<strong>' + inProgress.status + '</strong>). ' +
          '<a href="/jira/executions/' + inProgress.id + '?boardId=' + _claudeBoardID + '" class="underline font-medium">View logs →</a>';
        ipEl.classList.remove('hidden');
      }

      if (!ticketRes.success) { throw new Error(ticketRes.message || 'Failed to load ticket'); }
      var d = ticketRes.data;
      _claudeTicketDetail = d;
      document.getElementById('claude-field-summary').value  = d.summary || '';
      document.getElementById('claude-field-type').value     = d.type || '';
      document.getElementById('claude-field-priority').value = d.priority || '';
      document.getElementById('claude-field-sp').value       = d.story_points || '—';
      document.getElementById('claude-field-assignee').value = d.assignee || 'Unassigned';
      document.getElementById('claude-field-repo').value     = d.github_repo || '';
      document.getElementById('claude-field-base').value     = d.github_base || '';
      document.getElementById('claude-field-feature').value  = d.github_feature || _claudeTicketKey;
      var descEl = document.getElementById('claude-field-description');
      descEl.innerHTML = d.description || '<span class="text-gray-400 italic">No description</span>';
      document.getElementById('claude-ticket-fields').classList.remove('hidden');
      if (!inProgress) {
        document.getElementById('claude-submit').disabled = false;
      }
    })
    .catch(function(err) {
      document.getElementById('claude-loading').classList.add('hidden');
      document.getElementById('claude-fetch-error').textContent = 'Could not load ticket detail: ' + err.message;
      document.getElementById('claude-fetch-error').classList.remove('hidden');
    });
  }

  function closeClaudeModal() {
    document.getElementById('claude-modal').classList.add('hidden');
  }

  document.getElementById('claude-modal').addEventListener('click', function(e) {
    if (e.target === this) closeClaudeModal();
  });

  function parseDescriptionSections() {
    var descEl = document.getElementById('claude-field-description');
    var result = { whats: [], hows: [], acceptances: [] };
    var current = null;
    var children = descEl.children;
    for (var i = 0; i < children.length; i++) {
      var el = children[i];
      var tag = el.tagName.toLowerCase();
      if (/^h[1-6]$/.test(tag)) {
        var txt = el.textContent.toLowerCase();
        if (txt.indexOf('what') !== -1) current = 'whats';
        else if (txt.indexOf('how') !== -1) current = 'hows';
        else if (txt.indexOf('accept') !== -1) current = 'acceptances';
        else current = null;
      } else if (current) {
        if (tag === 'ul' || tag === 'ol') {
          var lis = el.querySelectorAll('li');
          for (var j = 0; j < lis.length; j++) {
            var t = lis[j].textContent.trim();
            if (t) result[current].push(t);
          }
        } else if (tag === 'p') {
          var pt = el.textContent.trim();
          if (pt) result[current].push(pt);
        }
      }
    }
    return result;
  }

  function submitClaudeRunner() {
    var sections = parseDescriptionSections();
    if (!sections.whats.length && !sections.hows.length && !sections.acceptances.length) return;

    var btn = document.getElementById('claude-submit');
    var spinner = document.getElementById('claude-spinner');
    var label = document.getElementById('claude-btn-label');
    var errorEl = document.getElementById('claude-error');

    btn.disabled = true;
    spinner.classList.remove('hidden');
    label.textContent = 'Queuing…';
    document.getElementById('claude-result').classList.add('hidden');
    errorEl.classList.add('hidden');

    fetch('/jira/boards/' + _claudeBoardID + '/execute', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({
        ticketId: _claudeTicketKey,
        repoUrl: _claudeTicketDetail.github_repo || '',
        baseBranch: _claudeTicketDetail.github_base || '',
        featureBranch: document.getElementById('claude-field-feature').value.trim(),
        prompt: {
          whats: sections.whats,
          hows: sections.hows,
          acceptances: sections.acceptances
        }
      })
    })
    .then(function(res) {
      return res.text().then(function(text) {
        var data;
        try { data = JSON.parse(text); } catch(e) { data = {result: text}; }
        return {ok: res.ok, data: data};
      });
    })
    .then(function(res) {
      if (res.ok && res.data.id) {
        window.location.href = '/jira/executions/' + res.data.id + '?boardId=' + _claudeBoardID;
      } else {
        errorEl.textContent = res.data.message || res.data.result || 'Execution failed.';
        errorEl.classList.remove('hidden');
        btn.disabled = false;
        spinner.classList.add('hidden');
        label.textContent = 'Execute';
      }
    })
    .catch(function(err) {
      errorEl.textContent = 'Network error: ' + err.message;
      errorEl.classList.remove('hidden');
      btn.disabled = false;
      spinner.classList.add('hidden');
      label.textContent = 'Execute';
    });
  }

  function viewTicketLogs(key) {
    window.location.href = '/jira/executions/ticket/' + key + '?boardId=' + _claudeBoardID;
  }
</script>

</body>
</html>`

var jiraLoginTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Sign in — Jira Boards</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <script>tailwind.config={darkMode:'class'}</script>
  <script>(function(){var t=localStorage.getItem('theme');if(t==='dark'||(!t&&window.matchMedia('(prefers-color-scheme:dark)').matches))document.documentElement.classList.add('dark');})()</script>
  <style>
    @keyframes fadeInUp {
      from { opacity: 0; transform: translateY(16px); }
      to   { opacity: 1; transform: translateY(0); }
    }
    .card-enter { animation: fadeInUp 0.3s ease-out both; }
    @keyframes spin { to { transform: rotate(360deg); } }
    .spinner { animation: spin 0.75s linear infinite; }
    .bg-dots {
      background-image: radial-gradient(circle, rgba(99,102,241,0.07) 1px, transparent 1px);
      background-size: 28px 28px;
    }
    .dark .bg-dots {
      background-image: radial-gradient(circle, rgba(99,102,241,0.13) 1px, transparent 1px);
    }
  </style>
</head>
<body class="bg-gray-50 dark:bg-gray-900 min-h-screen flex flex-col items-center justify-center px-4 bg-dots">

  <!-- Theme toggle -->
  <button onclick="toggleTheme()" class="absolute top-4 right-4 p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors" title="Toggle dark mode">
    <svg class="w-4 h-4 text-gray-500 dark:text-gray-400 dark:hidden" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z"/></svg>
    <svg class="w-4 h-4 text-gray-500 dark:text-gray-400 hidden dark:block" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364-6.364l-.707.707M6.343 17.657l-.707.707M17.657 17.657l-.707-.707M6.343 6.343l-.707-.707M16 12a4 4 0 11-8 0 4 4 0 018 0z"/></svg>
  </button>

  <!-- Card -->
  <div class="card-enter w-full max-w-sm">
    <div class="bg-white dark:bg-gray-800 rounded-2xl shadow-xl border border-gray-100 dark:border-gray-700 overflow-hidden">

      <!-- Card header accent -->
      <div class="h-1.5 w-full bg-gradient-to-r from-blue-500 via-blue-400 to-indigo-500"></div>

      <div class="px-8 pt-8 pb-10">
        <!-- Logo + title -->
        <div class="flex flex-col items-center mb-8">
          <div class="flex items-center justify-center w-14 h-14 bg-blue-50 dark:bg-blue-900/30 rounded-2xl mb-4 shadow-inner">
            <svg class="w-8 h-8 text-blue-600" fill="currentColor" viewBox="0 0 24 24">
              <path d="M11.571 11.513H0a5.218 5.218 0 0 0 5.232 5.215h2.13v2.057A5.215 5.215 0 0 0 12.575 24V12.518a1.005 1.005 0 0 0-1.004-1.005zm5.723-5.756H5.757a5.215 5.215 0 0 0 5.215 5.214h2.129v2.058a5.218 5.218 0 0 0 5.215 5.214V6.762a1.005 1.005 0 0 0-1.022-1.005zM23.013 0H11.455a5.215 5.215 0 0 0 5.215 5.215h2.129v2.057A5.215 5.215 0 0 0 24 12.486V1.005A1.005 1.005 0 0 0 23.013 0z"/>
            </svg>
          </div>
          <h1 class="text-xl font-bold text-gray-900 dark:text-gray-100 tracking-tight">Jira Boards</h1>
          <p class="text-sm text-gray-500 dark:text-gray-400 mt-1.5 text-center leading-relaxed">
            Sign in to view your team's<br class="hidden sm:block" /> sprint boards
          </p>
        </div>

        <!-- Divider -->
        <div class="border-t border-gray-100 dark:border-gray-700 mb-6"></div>

        <!-- Google sign-in button -->
        <a id="google-btn"
           href="/jira/auth/google?next={{.Next}}"
           onclick="setLoading(this)"
           class="inline-flex items-center justify-center gap-3 w-full px-5 py-3 rounded-xl border border-gray-200 dark:border-gray-600 bg-white dark:bg-gray-700 hover:bg-gray-50 dark:hover:bg-gray-600 hover:border-gray-300 dark:hover:border-gray-500 shadow-sm hover:shadow-md active:scale-[0.98] transition-all duration-150 text-sm font-medium text-gray-700 dark:text-gray-200 select-none">
          <!-- Google icon -->
          <svg id="google-icon" class="w-5 h-5 flex-shrink-0" viewBox="0 0 24 24">
            <path fill="#4285F4" d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"/>
            <path fill="#34A853" d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"/>
            <path fill="#FBBC05" d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l3.66-2.84z"/>
            <path fill="#EA4335" d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z"/>
          </svg>
          <!-- Loading spinner -->
          <svg id="spinner-icon" class="w-5 h-5 flex-shrink-0 hidden spinner text-blue-500" fill="none" viewBox="0 0 24 24">
            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/>
            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"/>
          </svg>
          <span id="btn-label">Continue with Google</span>
        </a>

        <!-- Access note -->
        <p class="text-xs text-gray-400 dark:text-gray-500 text-center mt-5 leading-relaxed">
          Access restricted to
          <span class="font-medium text-gray-500 dark:text-gray-400">@smmf.co.id</span> accounts
        </p>
      </div>
    </div>

    <!-- Footer -->
    <p class="text-xs text-gray-400 dark:text-gray-600 text-center mt-5">
      &copy; 2026 PT Sinarmas Multifinance
    </p>
  </div>

  <script>
    function toggleTheme() {
      const isDark = document.documentElement.classList.toggle('dark');
      localStorage.setItem('theme', isDark ? 'dark' : 'light');
    }
    function setLoading(el) {
      el.style.pointerEvents = 'none';
      el.style.opacity = '0.75';
      document.getElementById('google-icon').classList.add('hidden');
      document.getElementById('spinner-icon').classList.remove('hidden');
      document.getElementById('btn-label').textContent = 'Redirecting…';
    }
  </script>
</body>
</html>`

var jiraExecutionTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Execution · Claude Runner</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <script>
    tailwind.config = { darkMode: 'class' };
    (function() {
      var t = localStorage.getItem('theme');
      if (t === 'dark' || (!t && window.matchMedia('(prefers-color-scheme: dark)').matches)) {
        document.documentElement.classList.add('dark');
      }
    })();
  </script>
  <style>
    @keyframes log-fade-in {
      from { opacity: 0; transform: translateY(3px); }
      to   { opacity: 1; transform: translateY(0); }
    }
    @keyframes cursor-blink {
      0%%, 100%% { opacity: 0; }
      50%%        { opacity: 1; }
    }
    @keyframes bar-slide {
      0%%   { transform: translateX(-100%%); }
      100%% { transform: translateX(500%%); }
    }
    .log-line { animation: log-fade-in 0.12s ease-out both; line-height: 1.6; }
    .log-cursor::after {
      content: '...';
      display: inline-block;
      animation: cursor-blink 1.2s ease-in-out infinite;
      color: #a78bfa;
      margin-left: 2px;
      letter-spacing: 1px;
    }
    .log-bar-inner { animation: bar-slide 1.6s linear infinite; }
    .log-err  { color: #f87171; }
    .log-warn { color: #fbbf24; }
    .log-ok   { color: #34d399; }
    .log-dim  { color: #6b7280; }
    .log-cmd  { color: #60a5fa; }
  </style>
</head>
<body class="min-h-screen bg-gray-50 dark:bg-gray-950 text-gray-900 dark:text-gray-100 font-sans">
  <div class="max-w-4xl mx-auto px-4 py-8">

    <!-- Header -->
    <div class="flex items-center gap-3 mb-6">
      <a id="back-btn" href="/jira/boards" class="text-sm text-gray-500 hover:text-gray-900 dark:hover:text-gray-100 transition-colors">← Remaining Tasks</a>
      <span class="text-gray-300 dark:text-gray-600">/</span>
      <h1 class="text-lg font-semibold">Execution</h1>
      <span id="status-badge" class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400">loading…</span>
    </div>

    <!-- Metadata card -->
    <div class="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 mb-5">
      <div class="grid grid-cols-2 gap-x-8 gap-y-4 text-sm">
        <div>
          <div class="text-xs text-gray-400 uppercase tracking-wide mb-0.5">Ticket</div>
          <div id="meta-ticket" class="font-mono font-medium">—</div>
        </div>
        <div>
          <div class="text-xs text-gray-400 uppercase tracking-wide mb-0.5">Created</div>
          <div id="meta-created">—</div>
        </div>
        <div>
          <div class="text-xs text-gray-400 uppercase tracking-wide mb-0.5">Repository</div>
          <div id="meta-repo" class="font-mono truncate">—</div>
        </div>
        <div>
          <div class="text-xs text-gray-400 uppercase tracking-wide mb-0.5">Base Branch</div>
          <div id="meta-base" class="font-mono">—</div>
        </div>
        <div>
          <div class="text-xs text-gray-400 uppercase tracking-wide mb-0.5">Feature Branch</div>
          <div id="meta-feature" class="font-mono">—</div>
        </div>
        <div id="pr-row" class="hidden">
          <div class="text-xs text-gray-400 uppercase tracking-wide mb-0.5">Pull Request</div>
          <a id="pr-link" href="#" target="_blank" rel="noopener"
             class="text-violet-600 dark:text-violet-400 hover:underline font-mono text-xs">View PR →</a>
        </div>
      </div>
    </div>

    <!-- Log area -->
    <div class="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-gray-700">
        <div class="flex items-center gap-2">
          <h2 class="text-sm font-medium">Logs</h2>
          <span id="stream-dot" class="hidden w-2 h-2 rounded-full bg-violet-500 animate-pulse"></span>
        </div>
        <span id="log-status" class="text-xs text-gray-400">Connecting…</span>
      </div>
      <div id="log-progress" class="hidden relative h-0.5 bg-gray-200 dark:bg-gray-800 overflow-hidden">
        <div class="log-bar-inner absolute inset-y-0 left-0 w-1/4 bg-violet-500 rounded-full"></div>
      </div>
      <div id="log-area" class="font-mono text-xs text-green-400 bg-gray-950 p-4 h-[32rem] overflow-y-auto leading-relaxed m-0"></div>
    </div>

  </div>
  <script>
    var execID = '%s';
    var runnerURL = '%s';

    var statusColors = {
      pending: 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400',
      queued:  'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400',
      cloning: 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400',
      running: 'bg-violet-100 text-violet-700 dark:bg-violet-900/30 dark:text-violet-400',
      done:    'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400',
      failed:  'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400',
    };

    function setStatus(s) {
      var badge = document.getElementById('status-badge');
      badge.textContent = s;
      badge.className = 'inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ' +
        (statusColors[s] || 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400');
    }

    function showPRURL(url) {
      if (!url) return;
      var row = document.getElementById('pr-row');
      var link = document.getElementById('pr-link');
      row.classList.remove('hidden');
      link.href = url;
      link.textContent = url;
    }

    function loadMeta() {
      fetch('/jira/executions/' + execID + '/data')
        .then(function(r) { return r.json(); })
        .then(function(d) {
          document.getElementById('meta-ticket').textContent  = d.ticketId || '—';
          document.getElementById('meta-repo').textContent    = d.repoUrl || '—';
          document.getElementById('meta-base').textContent    = d.baseBranch || '—';
          document.getElementById('meta-feature').textContent = d.featureBranch || '—';
          document.getElementById('meta-created').textContent = d.createdAt
            ? new Date(d.createdAt).toLocaleString() : '—';
          setStatus(d.status || 'unknown');
          if (d.prUrl) showPRURL(d.prUrl);
        })
        .catch(function() {
          document.getElementById('log-status').textContent = 'Failed to load execution data.';
        });
    }

    function startLogs() {
      var logEl     = document.getElementById('log-area');
      var logStatus = document.getElementById('log-status');
      var streamDot = document.getElementById('stream-dot');
      var logProg   = document.getElementById('log-progress');
      var cursorEl  = null;
      var source    = new EventSource(runnerURL + '/api/executions/' + execID + '/logs');

      function stripAnsi(s) {
        return s.replace(/\x1b\[[0-9;]*[mGKHF]/g, '');
      }

      function lineClass(text) {
        var t = text.toLowerCase();
        if (/error|✗|failed|fatal/.test(t))          return 'log-err';
        if (/warn(ing)?/.test(t))                     return 'log-warn';
        if (/✓|success|done|completed|passed/.test(t)) return 'log-ok';
        if (/^(debug|\s*\/\/)/.test(t))               return 'log-dim';
        if (/^\s*(\$|>)\s/.test(text))                return 'log-cmd';
        return '';
      }

      function appendLine(text) {
        if (cursorEl) { logEl.removeChild(cursorEl); cursorEl = null; }
        var line = document.createElement('div');
        var cls  = lineClass(text);
        line.className = 'log-line' + (cls ? ' ' + cls : '');
        line.textContent = stripAnsi(text);
        logEl.appendChild(line);
        cursorEl = document.createElement('div');
        cursorEl.className = 'log-cursor';
        logEl.appendChild(cursorEl);
        logEl.scrollTop = logEl.scrollHeight;
      }

      function startStreaming() {
        streamDot.classList.remove('hidden');
        logProg.classList.remove('hidden');
        logStatus.textContent = 'Streaming…';
      }

      function stopStreaming(msg) {
        streamDot.classList.add('hidden');
        logProg.classList.add('hidden');
        if (cursorEl) { logEl.removeChild(cursorEl); cursorEl = null; }
        logStatus.textContent = msg;
      }

      source.addEventListener('log', function(e) {
        startStreaming();
        appendLine(e.data);
      });

      source.addEventListener('status', function(e) {
        setStatus(e.data);
        if (e.data === 'done' || e.data === 'failed') {
          source.close();
          stopStreaming(e.data === 'done' ? 'Completed' : 'Failed');
        } else {
          startStreaming();
        }
      });

      source.addEventListener('pr_url', function(e) {
        showPRURL(e.data);
      });

      source.onerror = function() {
        source.close();
        stopStreaming('Stream ended.');
      };
    }

    (function() {
      var boardId = new URLSearchParams(window.location.search).get('boardId');
      if (boardId) {
        document.getElementById('back-btn').href = '/jira/boards/' + boardId + '/remaining';
      }
    })();

    loadMeta();
    startLogs();
  </script>
</body>
</html>`

var jiraTicketExecutionsTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Executions</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <script>
    tailwind.config = { darkMode: 'class' };
    (function() {
      var t = localStorage.getItem('theme');
      if (t === 'dark' || (!t && window.matchMedia('(prefers-color-scheme: dark)').matches)) {
        document.documentElement.classList.add('dark');
      }
    })();
  </script>
  <style>
    @keyframes log-fade-in {
      from { opacity: 0; transform: translateY(3px); }
      to   { opacity: 1; transform: translateY(0); }
    }
    @keyframes cursor-blink {
      0%%, 100%% { opacity: 0; }
      50%%        { opacity: 1; }
    }
    @keyframes bar-slide {
      0%%   { transform: translateX(-100%%); }
      100%% { transform: translateX(500%%); }
    }
    .log-line { animation: log-fade-in 0.12s ease-out both; line-height: 1.6; }
    .log-cursor::after {
      content: '...';
      display: inline-block;
      animation: cursor-blink 1.2s ease-in-out infinite;
      color: #a78bfa;
      margin-left: 2px;
      letter-spacing: 1px;
    }
    .log-bar-inner { animation: bar-slide 1.6s linear infinite; }
    .log-err  { color: #f87171; }
    .log-warn { color: #fbbf24; }
    .log-ok   { color: #34d399; }
    .log-dim  { color: #6b7280; }
    .log-cmd  { color: #60a5fa; }
  </style>
</head>
<body class="min-h-screen bg-gray-50 dark:bg-gray-950 text-gray-900 dark:text-gray-100 font-sans">
  <div class="max-w-4xl mx-auto px-4 py-8">

    <!-- Header -->
    <div class="flex items-center gap-3 mb-6">
      <a id="back-btn" href="/jira/boards" class="text-sm text-gray-500 hover:text-gray-900 dark:hover:text-gray-100 transition-colors">← Remaining Tasks</a>
      <span class="text-gray-300 dark:text-gray-600">/</span>
      <span id="breadcrumb-key" class="text-sm font-mono font-semibold text-blue-500"></span>
      <div class="ml-auto flex items-center gap-2">
        <select id="exec-select" class="hidden text-xs border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 text-gray-700 dark:text-gray-300 rounded-lg px-2 py-1.5 cursor-pointer" onchange="loadExecution(this.value)"></select>
        <span id="status-badge" class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400">loading…</span>
      </div>
    </div>

    <!-- Empty state -->
    <div id="no-executions" class="hidden text-center py-20">
      <p class="text-gray-500 dark:text-gray-400 font-medium mb-1">No executions found</p>
      <p class="text-sm text-gray-400 dark:text-gray-500">Run Claude on this ticket to see logs here.</p>
    </div>

    <!-- Content (hidden until loaded) -->
    <div id="exec-content" class="hidden">

      <!-- Metadata card -->
      <div class="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 mb-5">
        <div class="grid grid-cols-2 gap-x-8 gap-y-4 text-sm">
          <div>
            <div class="text-xs text-gray-400 uppercase tracking-wide mb-0.5">Repository</div>
            <div id="meta-repo" class="font-mono truncate">—</div>
          </div>
          <div>
            <div class="text-xs text-gray-400 uppercase tracking-wide mb-0.5">Created</div>
            <div id="meta-created">—</div>
          </div>
          <div>
            <div class="text-xs text-gray-400 uppercase tracking-wide mb-0.5">Base Branch</div>
            <div id="meta-base" class="font-mono">—</div>
          </div>
          <div>
            <div class="text-xs text-gray-400 uppercase tracking-wide mb-0.5">Feature Branch</div>
            <div id="meta-feature" class="font-mono">—</div>
          </div>
          <div id="pr-row" class="hidden col-span-2">
            <div class="text-xs text-gray-400 uppercase tracking-wide mb-0.5">Pull Request</div>
            <a id="pr-link" href="#" target="_blank" rel="noopener"
               class="text-violet-600 dark:text-violet-400 hover:underline font-mono text-xs">View PR →</a>
          </div>
        </div>
      </div>

      <!-- Log area -->
      <div class="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl overflow-hidden">
        <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-gray-700">
          <div class="flex items-center gap-2">
            <h2 class="text-sm font-medium">Logs</h2>
            <span id="stream-dot" class="hidden w-2 h-2 rounded-full bg-violet-500 animate-pulse"></span>
          </div>
          <span id="log-status" class="text-xs text-gray-400">Connecting…</span>
        </div>
        <div id="log-progress" class="hidden relative h-0.5 bg-gray-200 dark:bg-gray-800 overflow-hidden">
          <div class="log-bar-inner absolute inset-y-0 left-0 w-1/4 bg-violet-500 rounded-full"></div>
        </div>
        <div id="log-area" class="font-mono text-xs text-gray-100 bg-gray-950 p-4 h-[32rem] overflow-y-auto leading-relaxed m-0"></div>
      </div>

    </div>
  </div>

  <script>
    var ticketKey = '%s';
    var runnerURL = '%s';
    var allExecutions = [];
    var currentSource = null;

    document.title = 'Executions · ' + ticketKey;
    document.getElementById('breadcrumb-key').textContent = ticketKey;

    (function() {
      var boardId = new URLSearchParams(window.location.search).get('boardId');
      if (boardId) document.getElementById('back-btn').href = '/jira/boards/' + boardId + '/remaining';
    })();

    var statusColors = {
      pending: 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400',
      queued:  'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400',
      cloning: 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400',
      running: 'bg-violet-100 text-violet-700 dark:bg-violet-900/30 dark:text-violet-400',
      done:    'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400',
      failed:  'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400',
    };

    function setStatus(s) {
      var badge = document.getElementById('status-badge');
      badge.textContent = s;
      badge.className = 'inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ' +
        (statusColors[s] || 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400');
    }

    function showPRURL(url) {
      if (!url) return;
      document.getElementById('pr-row').classList.remove('hidden');
      var link = document.getElementById('pr-link');
      link.href = url;
      link.textContent = url;
    }

    function loadExecution(execID) {
      if (currentSource) { currentSource.close(); currentSource = null; }

      var exec = null;
      for (var i = 0; i < allExecutions.length; i++) {
        if (allExecutions[i].id === execID) { exec = allExecutions[i]; break; }
      }
      if (!exec) return;

      document.getElementById('meta-repo').textContent    = exec.repoUrl || '—';
      document.getElementById('meta-base').textContent    = exec.baseBranch || '—';
      document.getElementById('meta-feature').textContent = exec.featureBranch || '—';
      document.getElementById('meta-created').textContent = exec.createdAt
        ? new Date(exec.createdAt).toLocaleString() : '—';
      document.getElementById('pr-row').classList.add('hidden');
      setStatus(exec.status || 'unknown');
      if (exec.prUrl) showPRURL(exec.prUrl);

      var logEl     = document.getElementById('log-area');
      var logStatus = document.getElementById('log-status');
      var streamDot = document.getElementById('stream-dot');
      var logProg   = document.getElementById('log-progress');
      var cursorEl  = null;

      logEl.innerHTML = '';
      logStatus.textContent = 'Connecting…';

      function stripAnsi(s) {
        return s.replace(/\x1b\[[0-9;]*[mGKHF]/g, '');
      }

      function lineClass(text) {
        var t = text.toLowerCase();
        if (/error|✗|failed|fatal/.test(t))           return 'log-err';
        if (/warn(ing)?/.test(t))                      return 'log-warn';
        if (/✓|success|done|completed|passed/.test(t)) return 'log-ok';
        if (/^(debug|\s*\/\/)/.test(t))                return 'log-dim';
        if (/^\s*(\$|>)\s/.test(text))                 return 'log-cmd';
        return '';
      }

      function appendLine(text) {
        if (cursorEl) { logEl.removeChild(cursorEl); cursorEl = null; }
        var line = document.createElement('div');
        var cls  = lineClass(text);
        line.className = 'log-line' + (cls ? ' ' + cls : '');
        line.textContent = stripAnsi(text);
        logEl.appendChild(line);
        cursorEl = document.createElement('div');
        cursorEl.className = 'log-cursor';
        logEl.appendChild(cursorEl);
        logEl.scrollTop = logEl.scrollHeight;
      }

      function startStreaming() {
        streamDot.classList.remove('hidden');
        logProg.classList.remove('hidden');
        logStatus.textContent = 'Streaming…';
      }

      function stopStreaming(msg) {
        streamDot.classList.add('hidden');
        logProg.classList.add('hidden');
        if (cursorEl) { logEl.removeChild(cursorEl); cursorEl = null; }
        logStatus.textContent = msg;
      }

      var source = new EventSource(runnerURL + '/api/executions/' + execID + '/logs');
      currentSource = source;

      source.addEventListener('log', function(e) {
        startStreaming();
        appendLine(e.data);
      });

      source.addEventListener('status', function(e) {
        setStatus(e.data);
        if (e.data === 'done' || e.data === 'failed') {
          source.close(); currentSource = null;
          stopStreaming(e.data === 'done' ? 'Completed' : 'Failed');
        } else {
          startStreaming();
        }
      });

      source.addEventListener('pr_url', function(e) { showPRURL(e.data); });

      source.onerror = function() {
        source.close(); currentSource = null;
        stopStreaming('Stream ended.');
      };
    }

    function init() {
      fetch('/jira/executions?ticketId=' + ticketKey)
        .then(function(r) { return r.json(); })
        .then(function(list) {
          if (!list || !list.length) {
            document.getElementById('no-executions').classList.remove('hidden');
            document.getElementById('status-badge').classList.add('hidden');
            return;
          }

          list.sort(function(a, b) { return new Date(b.createdAt) - new Date(a.createdAt); });
          allExecutions = list;

          var sel = document.getElementById('exec-select');
          list.forEach(function(e, i) {
            var opt = document.createElement('option');
            opt.value = e.id;
            opt.textContent = new Date(e.createdAt).toLocaleString() + ' · ' + e.status;
            sel.appendChild(opt);
          });
          if (list.length > 1) sel.classList.remove('hidden');

          document.getElementById('exec-content').classList.remove('hidden');
          loadExecution(list[0].id);
        })
        .catch(function() {
          document.getElementById('no-executions').classList.remove('hidden');
          document.getElementById('status-badge').classList.add('hidden');
        });
    }

    init();
  </script>
</body>
</html>`
