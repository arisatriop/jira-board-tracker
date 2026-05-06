package handler

import (
	"bytes"
	"html/template"
	"math"
	"strconv"
	"sync"
	"time"

	"project-tracker/pkg/jira"
	"project-tracker/pkg/response"

	"github.com/gofiber/fiber/v2"
)

type Jira struct {
	client *jira.Client
	apiKey string
}

func NewJira(client *jira.Client, apiKey string) *Jira {
	return &Jira{client: client, apiKey: apiKey}
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

type BoardWithSprint struct {
	jira.Board
	ActiveSprint *jira.Sprint
	DaysLeft     int
	Overdue      bool
	HasEndDate   bool
	SprintStats  jira.SprintStats
}

func sprintTimeline(sprint *jira.Sprint) (daysLeft int, overdue bool, hasEndDate bool) {
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
		Boards []BoardWithSprint
		Error  string
		APIKey string
	}

	data := viewData{APIKey: h.apiKey}

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
				if b.Type == "scrum" {
					wg.Add(1)
					go func(idx int, boardID int) {
						defer wg.Done()
						sprint, _ := h.client.GetActiveSprint(ctx.Context(), boardID)
						results[idx].ActiveSprint = sprint
						results[idx].DaysLeft, results[idx].Overdue, results[idx].HasEndDate = sprintTimeline(sprint)
						if sprint != nil {
							stats, _ := h.client.GetSprintStats(ctx.Context(), sprint.ID)
							results[idx].SprintStats = stats
						}
					}(i, b.ID)
				}
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
  <style>
    .filter-btn { background: white; border-color: #e5e7eb; color: #6b7280; }
    .filter-btn:hover { border-color: #3b82f6; color: #3b82f6; }
    .filter-btn.active { background: #3b82f6; border-color: #3b82f6; color: white; }
  </style>
</head>
<body class="bg-gray-50 min-h-screen">

  <div class="max-w-5xl mx-auto px-6 py-10">

    <!-- Header -->
    <div class="flex items-center gap-3 mb-8">
      <svg class="w-8 h-8 text-blue-600" fill="currentColor" viewBox="0 0 24 24">
        <path d="M11.571 11.513H0a5.218 5.218 0 0 0 5.232 5.215h2.13v2.057A5.215 5.215 0 0 0 12.575 24V12.518a1.005 1.005 0 0 0-1.004-1.005zm5.723-5.756H5.757a5.215 5.215 0 0 0 5.215 5.214h2.129v2.058a5.218 5.218 0 0 0 5.215 5.214V6.762a1.005 1.005 0 0 0-1.022-1.005zM23.013 0H11.455a5.215 5.215 0 0 0 5.215 5.215h2.129v2.057A5.215 5.215 0 0 0 24 12.486V1.005A1.005 1.005 0 0 0 23.013 0z"/>
      </svg>
      <div>
        <h1 class="text-2xl font-bold text-gray-900">Jira Boards</h1>
        <p class="text-sm text-gray-500">All boards from your Jira workspace</p>
      </div>
    </div>

    <!-- Error state -->
    {{if .Error}}
    <div class="bg-red-50 border border-red-200 rounded-lg p-4 flex items-start gap-3">
      <svg class="w-5 h-5 text-red-500 mt-0.5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/>
      </svg>
      <p class="text-sm text-red-700">{{.Error}}</p>
    </div>
    {{end}}

    <!-- Empty state -->
    {{if and (not .Error) (eq (len .Boards) 0)}}
    <div class="text-center py-20 text-gray-400">
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
               class="w-full pl-10 pr-4 py-2.5 text-sm bg-white border border-gray-200 rounded-xl shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent placeholder-gray-400" />
      </div>
      <div class="flex items-center gap-2 flex-wrap">
        <span class="text-xs text-gray-400 font-medium">Type</span>
        <button onclick="setType('all')"    id="filter-all"    class="filter-btn active px-3 py-2 text-sm font-medium rounded-xl border transition-colors">All</button>
        <button onclick="setType('scrum')"  id="filter-scrum"  class="filter-btn px-3 py-2 text-sm font-medium rounded-xl border transition-colors">Scrum</button>
        <button onclick="setType('kanban')" id="filter-kanban" class="filter-btn px-3 py-2 text-sm font-medium rounded-xl border transition-colors">Kanban</button>

        <span class="text-xs text-gray-400 font-medium ml-2">Sprint</span>
        <button onclick="setSprint('all')"      id="sprint-all"      class="filter-btn sprint-btn active px-3 py-2 text-sm font-medium rounded-xl border transition-colors">All</button>
        <button onclick="setSprint('active')"   id="sprint-active"   class="filter-btn sprint-btn px-3 py-2 text-sm font-medium rounded-xl border transition-colors">Active</button>
        <button onclick="setSprint('overdue')"  id="sprint-overdue"  class="filter-btn sprint-btn px-3 py-2 text-sm font-medium rounded-xl border transition-colors">Overdue</button>
        <button onclick="setSprint('none')"     id="sprint-none"     class="filter-btn sprint-btn px-3 py-2 text-sm font-medium rounded-xl border transition-colors">No Sprint</button>
      </div>
    </div>
    <div class="flex items-center justify-between mb-4">
      <p id="board-count" class="text-sm text-gray-500">{{len .Boards}} board{{if gt (len .Boards) 1}}s{{end}} found</p>
      <div class="flex items-center gap-2 text-sm text-gray-500">
        <span>Per page:</span>
        <select id="page-size" onchange="changePageSize()" class="text-sm bg-white border border-gray-200 rounded-lg px-2 py-1 focus:outline-none focus:ring-2 focus:ring-blue-500">
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
           class="bg-white rounded-xl border border-gray-200 shadow-sm hover:shadow-md hover:border-blue-300 transition-all cursor-pointer p-5 group">
        <div class="flex items-start justify-between gap-2 mb-3">
          <h2 class="text-base font-semibold text-gray-800 group-hover:text-blue-600 leading-snug transition-colors">{{.Name}}</h2>
          {{if eq .Type "scrum"}}
          <span class="text-xs font-medium bg-blue-100 text-blue-700 px-2 py-0.5 rounded-full whitespace-nowrap">Scrum</span>
          {{else if eq .Type "kanban"}}
          <span class="text-xs font-medium bg-green-100 text-green-700 px-2 py-0.5 rounded-full whitespace-nowrap">Kanban</span>
          {{else}}
          <span class="text-xs font-medium bg-gray-100 text-gray-600 px-2 py-0.5 rounded-full whitespace-nowrap">{{.Type}}</span>
          {{end}}
        </div>
        {{if .ActiveSprint}}
        {{$total := .SprintStats.Total}}
        {{$done  := .SprintStats.Done}}
        {{if gt $total 0}}
        <div class="mt-3 mb-1">
          <div class="flex items-center justify-between mb-1.5">
            <span class="text-xs text-gray-500">{{$done}}/{{$total}} done</span>
            <span class="text-xs font-medium {{if eq $done $total}}text-emerald-600{{else}}text-gray-400{{end}}">
              {{if gt $total 0}}{{pct $done $total}}%{{end}}
            </span>
          </div>
          <div class="w-full h-1.5 bg-gray-100 rounded-full overflow-hidden">
            <div class="h-full rounded-full {{if eq $done $total}}bg-emerald-500{{else if .Overdue}}bg-red-400{{else}}bg-blue-500{{end}} transition-all"
                 style="width: {{pct $done $total}}%"></div>
          </div>
        </div>
        {{end}}
        {{end}}
        <div class="flex items-center justify-between mt-2">
          <p class="text-xs text-gray-400">ID: {{.ID}}</p>
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
          <span class="text-xs text-gray-400 italic">No active sprint</span>
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
       class="fixed right-0 top-0 h-full w-full max-w-lg bg-white shadow-2xl z-50 translate-x-full transition-transform duration-300 overflow-y-auto">

    <div class="sticky top-0 bg-white border-b border-gray-100 px-6 py-4 flex items-center justify-between z-10">
      <div>
        <h2 id="modal-title" class="text-lg font-semibold text-gray-900"></h2>
        <p class="text-xs text-gray-400 mt-0.5">Board Summary</p>
      </div>
      <button onclick="closeModal()" class="p-2 rounded-lg hover:bg-gray-100 transition-colors">
        <svg class="w-5 h-5 text-gray-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
        </svg>
      </button>
    </div>

    <!-- Loading -->
    <div id="modal-loading" class="flex flex-col items-center justify-center py-24 gap-3 text-gray-400">
      <svg class="w-8 h-8 animate-spin" fill="none" viewBox="0 0 24 24">
        <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/>
        <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z"/>
      </svg>
      <p class="text-sm">Loading summary...</p>
    </div>

    <!-- Error -->
    <div id="modal-error" class="hidden px-6 py-8">
      <div class="bg-red-50 border border-red-200 rounded-lg p-4 text-sm text-red-700"></div>
    </div>

    <!-- Content -->
    <div id="modal-content" class="hidden px-6 py-6 space-y-6"></div>
  </div>

  <script>
    const API_KEY = '{{.APIKey}}';

    let activeType   = 'all';
    let activeSprint = 'all';
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
      const btnInactive = 'bg-white border-gray-200 text-gray-600 hover:border-blue-400 hover:text-blue-600';
      const btnDisabled = 'bg-white border-gray-100 text-gray-300 cursor-not-allowed';

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

    // initialise on load
    window.addEventListener('DOMContentLoaded', function() { applyFilters(); });

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
      html += '<div class="flex items-center gap-2">' + typeBadge + '<span class="text-xs text-gray-400">Board ID: ' + data.board.id + '</span></div>';

      // Active sprint
      if (data.active_sprint) {
        const s = data.active_sprint;
        const ss = data.sprint_stats || {};
        const total = ss.Total || 0;
        const done  = ss.Done  || 0;
        const pct   = total > 0 ? Math.round(done / total * 100) : 0;
        const isOverdue = s.endDate && new Date(s.endDate) < new Date();
        const barColor = pct === 100 ? 'bg-emerald-500' : isOverdue ? 'bg-red-400' : 'bg-blue-500';

        const overdueLabel = isOverdue
          ? '<span class="text-xs font-medium bg-red-100 text-red-600 px-2 py-1 rounded-full whitespace-nowrap">Overdue</span>'
          : '<span class="text-xs font-medium bg-emerald-100 text-emerald-700 px-2 py-1 rounded-full whitespace-nowrap">On Track</span>';
        const goalHtml   = s.goal      ? '<p class="text-sm text-gray-600 italic">&ldquo;' + esc(s.goal) + '&rdquo;</p>' : '';
        const startHtml  = s.startDate ? '<span>Start: ' + s.startDate.slice(0,10) + '</span>' : '';
        const endHtml    = s.endDate   ? '<span>End: '   + s.endDate.slice(0,10)   + '</span>' : '';
        const pctColor   = pct === 100 ? 'text-emerald-600' : 'text-gray-500';
        const progressHtml = total > 0
          ? '<div><div class="flex justify-between text-xs mb-1.5"><span class="text-gray-600 font-medium">' + done + '/' + total + ' done</span><span class="font-semibold ' + pctColor + '">' + pct + '%</span></div><div class="w-full h-2 bg-white rounded-full overflow-hidden border border-blue-100"><div class="h-full rounded-full ' + barColor + ' transition-all" style="width:' + pct + '%"></div></div></div>'
          : '';

        html += '<div class="bg-blue-50 border border-blue-200 rounded-xl p-4 space-y-3">'
          + '<div class="flex items-start justify-between gap-2"><div>'
          + '<p class="text-xs font-semibold text-blue-500 uppercase tracking-wide">Active Sprint</p>'
          + '<p class="text-base font-semibold text-gray-800 mt-0.5">' + esc(s.name) + '</p>'
          + '</div>' + overdueLabel + '</div>'
          + goalHtml
          + '<div class="flex gap-4 text-xs text-gray-500">' + startHtml + endHtml + '</div>'
          + progressHtml
          + '</div>';
      }

      // Status breakdown
      if (data.status_stats && Object.keys(data.status_stats).length > 0) {
        const statusColor = s => ({
          'To Do': 'bg-gray-100 text-gray-600',
          'In Progress': 'bg-blue-100 text-blue-700',
          'In Review': 'bg-yellow-100 text-yellow-700',
          'Done': 'bg-emerald-100 text-emerald-700',
        }[s] || 'bg-gray-100 text-gray-500');

        html += '<div><p class="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">By Status <span class="normal-case font-normal text-gray-400">(' + data.total_issues + ' total)</span></p><div class="flex flex-wrap gap-2">';
        for (const [s, n] of Object.entries(data.status_stats)) {
          html += '<span class="text-xs font-medium px-2.5 py-1 rounded-full ' + statusColor(s) + '">' + esc(s) + ': ' + n + '</span>';
        }
        html += '</div></div>';
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

        html += '<div><p class="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">By Type</p><div class="flex flex-wrap gap-2">';
        for (const [t, n] of Object.entries(data.type_stats)) {
          html += '<span class="text-xs font-medium px-2.5 py-1 rounded-full border ' + typeColor(t) + '">' + esc(t) + ': ' + n + '</span>';
        }
        html += '</div></div>';
      }

      // Assignee breakdown
      if (data.assignee_stats && data.assignee_stats.length > 0) {
        const sorted = [...data.assignee_stats].sort((a, b) => b.count - a.count);
        html += '<div><p class="text-xs font-semibold text-gray-500 uppercase tracking-wide mb-2">By Assignee</p><div class="space-y-2">';
        sorted.forEach(function(a) {
          const initials = a.display_name.split(' ').map(function(w){return w[0];}).join('').slice(0,2).toUpperCase();
          const avatar = a.avatar_url
            ? '<img src="' + esc(a.avatar_url) + '" class="w-7 h-7 rounded-full object-cover" />'
            : '<div class="w-7 h-7 rounded-full bg-blue-100 text-blue-700 text-xs font-bold flex items-center justify-center">' + initials + '</div>';
          html += '<div class="flex items-center gap-3">' + avatar
            + '<span class="text-sm text-gray-700 flex-1">' + esc(a.display_name) + '</span>'
            + '<span class="text-xs font-medium text-purple-700 bg-purple-100 px-2 py-0.5 rounded-full" title="Story Points">' + a.story_points + ' SP</span>'
            + '<span class="text-xs font-semibold text-gray-500 bg-gray-100 px-2 py-0.5 rounded-full">' + a.count + '</span>'
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

        html += '<div class="bg-amber-50 border border-amber-200 rounded-xl p-4 space-y-3">'
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
        const undoneAssignees = Object.values(undoneAssigneeMap).sort(function(a, b) { return b.count - a.count; });
        if (undoneAssignees.length > 0) {
          html += '<div><p class="text-xs text-amber-600 font-medium mb-1.5">By Assignee</p><div class="space-y-2">';
          undoneAssignees.forEach(function(a) {
            const initials = a.display_name.split(' ').map(function(w) { return w[0]; }).join('').slice(0, 2).toUpperCase();
            const avatar = a.avatar_url
              ? '<img src="' + esc(a.avatar_url) + '" class="w-7 h-7 rounded-full object-cover" />'
              : '<div class="w-7 h-7 rounded-full bg-amber-100 text-amber-700 text-xs font-bold flex items-center justify-center">' + initials + '</div>';
            html += '<div class="flex items-center gap-3">' + avatar
              + '<span class="text-sm text-gray-700 flex-1">' + esc(a.display_name) + '</span>'
              + '<span class="text-xs font-medium text-purple-700 bg-purple-100 px-2 py-0.5 rounded-full" title="Story Points">' + a.story_points + ' SP</span>'
              + '<span class="text-xs font-semibold text-amber-700 bg-amber-100 px-2 py-0.5 rounded-full">' + a.count + '</span>'
              + '</div>';
          });
          html += '</div></div>';
        }

        html += '<div class="border-t border-amber-200 pt-3 mt-1">'
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
          + '<p class="text-xs font-semibold text-gray-500 uppercase tracking-wide">Issues</p>'
          + '<span id="issues-count" class="text-xs text-gray-400"></span>'
          + '</div>'
          + '<div id="issues-list" class="space-y-1.5"></div>'
          + '<div id="issues-pagination" class="flex items-center justify-center gap-1 mt-3"></div>'
          + '</div>';

        content.innerHTML = html;
        initIssuesPagination(data.issues);
        return;
      }

      if (!html) {
        html = '<p class="text-sm text-gray-400 text-center py-8">No data available for this board.</p>';
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
          const typeHtml = type ? '<span class="text-xs text-gray-400">' + esc(type) + '</span>' : '';
          const assigneeHtml = issue.fields.assignee
            ? '<span class="text-xs text-gray-400">&middot; ' + esc(issue.fields.assignee.displayName) + '</span>'
            : '<span class="text-xs text-gray-300">&middot; Unassigned</span>';
          const prioHtml = prio ? '<span class="text-xs font-medium ' + priorityColor(prio) + ' ml-auto">' + esc(prio) + '</span>' : '';
          return '<div class="flex items-start gap-2.5 p-2.5 rounded-lg bg-gray-50 hover:bg-gray-100 transition-colors">'
            + '<span class="w-2 h-2 rounded-full mt-1.5 flex-shrink-0 ' + statusDot(issue.fields.status.name) + '"></span>'
            + '<div class="flex-1 min-w-0">'
            + '<div class="flex items-center gap-1.5 mb-0.5"><span class="text-xs font-mono font-semibold text-blue-600">' + esc(issue.key) + '</span>' + typeHtml + '</div>'
            + '<p class="text-sm text-gray-800 truncate">' + esc(issue.fields.summary) + '</p>'
            + '<div class="flex items-center gap-2 mt-1"><span class="text-xs text-gray-500">' + esc(issue.fields.status.name) + '</span>' + assigneeHtml + prioHtml + '</div>'
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
        const btnInactive = 'bg-white border-gray-200 text-gray-600 hover:border-blue-400 hover:text-blue-600';
        const btnDisabled = 'bg-white border-gray-100 text-gray-300 cursor-not-allowed';

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
  </script>

  <div id="pill-tooltip" class="fixed z-[200] hidden bg-gray-900 text-white text-xs rounded-lg px-3 py-2 shadow-xl pointer-events-none space-y-1">
    <div id="pill-tooltip-inner"></div>
  </div>

</body>
</html>`

type issueNode struct {
	Issue    jira.Issue
	Children []jira.Issue
}

func (h *Jira) RemainingView(ctx *fiber.Ctx) error {
	type viewData struct {
		Board       jira.Board
		Sprint      *jira.Sprint
		SprintStats jira.SprintStats
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
				var remaining []jira.Issue
				for _, issue := range summary.Issues {
					if issue.Fields.Status.StatusCategory.Key != "done" {
						remaining = append(remaining, issue)
					}
				}
				data.Total = len(remaining)

				// all issues are top-level; attach matching undone children beneath parents
				issueMap := make(map[string]jira.Issue, len(remaining))
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

var remainingTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Remaining Work{{if .Board.Name}} — {{.Board.Name}}{{end}}</title>
  <script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="bg-gray-50 min-h-screen">

<div class="max-w-6xl mx-auto px-6 py-8">

  <!-- Back nav -->
  <a href="/jira/boards" class="inline-flex items-center gap-1.5 text-sm text-gray-500 hover:text-blue-600 mb-6 transition-colors group">
    <svg class="w-4 h-4 group-hover:-translate-x-0.5 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7"/>
    </svg>
    Back to Boards
  </a>

  {{if .Error}}
  <div class="bg-red-50 border border-red-200 rounded-lg p-4 text-sm text-red-700 mb-6">{{.Error}}</div>
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
        <span class="text-xs text-gray-400">Board {{.Board.ID}}</span>
      </div>
      <h1 class="text-2xl font-bold text-gray-900">{{.Board.Name}}</h1>
      <p class="text-sm text-gray-500 mt-0.5">Remaining work</p>
    </div>
    <div class="text-right flex-shrink-0">
      <p class="text-3xl font-bold text-amber-600">{{.Total}}</p>
      <p class="text-sm text-gray-400">remaining tickets</p>
    </div>
  </div>

  <!-- Sprint card -->
  {{if .Sprint}}
  {{$done  := .SprintStats.Done}}
  {{$total := .SprintStats.Total}}
  <div class="bg-white border border-gray-200 rounded-xl p-5 mb-6 shadow-sm">
    <div class="flex items-start justify-between gap-4 mb-3">
      <div>
        <p class="text-xs font-semibold text-blue-500 uppercase tracking-wide mb-0.5">Active Sprint</p>
        <p class="text-base font-semibold text-gray-800">{{.Sprint.Name}}</p>
        {{if .Sprint.Goal}}<p class="text-sm text-gray-500 italic mt-0.5">&ldquo;{{.Sprint.Goal}}&rdquo;</p>{{end}}
      </div>
      {{if gt $total 0}}
      <div class="text-right flex-shrink-0">
        <p class="text-2xl font-bold text-gray-800">{{pct $done $total}}<span class="text-base font-normal text-gray-400">%</span></p>
        <p class="text-xs text-gray-500">{{$done}}/{{$total}} done</p>
      </div>
      {{end}}
    </div>
    {{if gt $total 0}}
    <div class="w-full h-2 bg-gray-100 rounded-full overflow-hidden">
      <div class="h-full rounded-full bg-blue-500" style="width:{{pct $done $total}}%"></div>
    </div>
    {{end}}
    {{if or .Sprint.StartDate .Sprint.EndDate}}
    <div class="flex gap-4 text-xs text-gray-400 mt-2">
      {{if .Sprint.StartDate}}<span>Start: {{dateShort .Sprint.StartDate}}</span>{{end}}
      {{if .Sprint.EndDate}}<span>End: {{dateShort .Sprint.EndDate}}</span>{{end}}
    </div>
    {{end}}
  </div>
  {{end}}

  {{if .HasMore}}
  <div class="bg-amber-50 border border-amber-200 rounded-lg px-4 py-3 text-sm text-amber-700 mb-4">
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
             class="w-full pl-10 pr-4 py-2.5 text-sm bg-white border border-gray-200 rounded-xl shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500 placeholder-gray-400" />
    </div>
    <select id="filter-status"   onchange="applyFilters()" class="text-sm bg-white border border-gray-200 rounded-xl px-3 py-2.5 shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
      <option value="">All Statuses</option>
    </select>
    <select id="filter-type"     onchange="applyFilters()" class="text-sm bg-white border border-gray-200 rounded-xl px-3 py-2.5 shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
      <option value="">All Types</option>
    </select>
    <select id="filter-assignee" onchange="applyFilters()" class="text-sm bg-white border border-gray-200 rounded-xl px-3 py-2.5 shadow-sm focus:outline-none focus:ring-2 focus:ring-blue-500">
      <option value="">All Assignees</option>
    </select>
  </div>
  <p id="ticket-count" class="text-sm text-gray-500 mb-3">{{.Total}} ticket{{if gt .Total 1}}s{{end}} remaining</p>

  <!-- Ticket list -->
  <div id="ticket-list" class="space-y-1.5">
    {{range .Nodes}}
    <div data-node
         data-status="{{.Issue.Fields.Status.Name}}"
         data-type="{{.Issue.Fields.IssueType.Name}}"
         data-assignee="{{if .Issue.Fields.Assignee}}{{.Issue.Fields.Assignee.DisplayName}}{{else}}Unassigned{{end}}"
         data-key="{{.Issue.Key}}"
         data-summary="{{.Issue.Fields.Summary}}">

      <!-- Parent row -->
      <div class="flex items-center gap-3 px-4 py-3 bg-white rounded-xl border border-gray-100 hover:border-blue-200 hover:shadow-sm transition-all {{if .Children}}cursor-pointer{{end}}"
           {{if .Children}}onclick="toggleChildren('{{.Issue.Key}}')"{{end}}>
        {{if .Children}}
        <svg id="chevron-{{.Issue.Key}}" class="w-4 h-4 text-gray-400 flex-shrink-0 transition-transform duration-150" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/>
        </svg>
        {{else}}
        <span class="w-2.5 h-2.5 rounded-full flex-shrink-0 {{statusDotClass .Issue.Fields.Status.Name}}"></span>
        {{end}}
        <span class="text-xs font-mono font-semibold text-blue-600 w-28 flex-shrink-0">{{.Issue.Key}}</span>
        {{if .Issue.Fields.IssueType.Name}}
        <span class="text-xs font-medium px-2 py-0.5 rounded-full border flex-shrink-0 {{typeBadgeClass .Issue.Fields.IssueType.Name}}">{{.Issue.Fields.IssueType.Name}}</span>
        {{end}}
        <span class="text-sm text-gray-800 flex-1 min-w-0">{{.Issue.Fields.Summary}}</span>
        {{if .Children}}
        <span class="text-xs text-gray-400 flex-shrink-0 bg-gray-100 px-2 py-0.5 rounded-full">{{len .Children}} sub-task{{if gt (len .Children) 1}}s{{end}}</span>
        {{end}}
        <span class="text-xs text-gray-500 flex-shrink-0 w-36 text-right truncate">
          {{if .Issue.Fields.Assignee}}{{.Issue.Fields.Assignee.DisplayName}}{{else}}<span class="text-gray-300">Unassigned</span>{{end}}
        </span>
        {{if .Issue.Fields.Priority}}
        <span class="text-xs font-medium flex-shrink-0 w-16 text-right {{priorityClass .Issue.Fields.Priority.Name}}">{{.Issue.Fields.Priority.Name}}</span>
        {{end}}
        <span class="text-xs text-gray-400 flex-shrink-0 w-28 text-right">{{.Issue.Fields.Status.Name}}</span>
      </div>

      <!-- Children (hidden by default) -->
      {{if .Children}}
      <div id="children-{{.Issue.Key}}" class="hidden ml-8 mt-1 space-y-1">
        {{range .Children}}
        <div class="flex items-center gap-3 px-4 py-2.5 bg-gray-50 rounded-xl border border-gray-100 hover:bg-gray-100 transition-all"
             data-child
             data-status="{{.Fields.Status.Name}}"
             data-type="{{.Fields.IssueType.Name}}"
             data-assignee="{{if .Fields.Assignee}}{{.Fields.Assignee.DisplayName}}{{else}}Unassigned{{end}}"
             data-key="{{.Key}}"
             data-summary="{{.Fields.Summary}}">
          <span class="w-2 h-2 rounded-full flex-shrink-0 {{statusDotClass .Fields.Status.Name}}"></span>
          <span class="text-xs font-mono font-semibold text-blue-500 w-28 flex-shrink-0">{{.Key}}</span>
          {{if .Fields.IssueType.Name}}
          <span class="text-xs font-medium px-2 py-0.5 rounded-full border flex-shrink-0 {{typeBadgeClass .Fields.IssueType.Name}}">{{.Fields.IssueType.Name}}</span>
          {{end}}
          <span class="text-sm text-gray-700 flex-1 min-w-0">{{.Fields.Summary}}</span>
          <span class="text-xs text-gray-400 flex-shrink-0 w-36 text-right truncate">
            {{if .Fields.Assignee}}{{.Fields.Assignee.DisplayName}}{{else}}<span class="text-gray-300">Unassigned</span>{{end}}
          </span>
          {{if .Fields.Priority}}
          <span class="text-xs font-medium flex-shrink-0 w-16 text-right {{priorityClass .Fields.Priority.Name}}">{{.Fields.Priority.Name}}</span>
          {{end}}
          <span class="text-xs text-gray-400 flex-shrink-0 w-28 text-right">{{.Fields.Status.Name}}</span>
        </div>
        {{end}}
      </div>
      {{end}}

    </div>
    {{end}}
  </div>

  {{else}}
  <div class="text-center py-24 text-gray-400">
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
    nodes.forEach(function(node) {
      const matchSearch   = !q      || (node.dataset.key + ' ' + node.dataset.summary).toLowerCase().includes(q);
      const matchStatus   = !status   || node.dataset.status   === status;
      const matchType     = !type     || node.dataset.type     === type;
      const matchAssignee = !assignee || node.dataset.assignee === assignee;
      const show = matchSearch && matchStatus && matchType && matchAssignee;
      node.style.display = show ? '' : 'none';
      if (show) visible++;
    });
    const el = document.getElementById('ticket-count');
    if (el) el.textContent = visible + ' ticket' + (visible !== 1 ? 's' : '') + ' remaining';
  }
</script>

</body>
</html>`
