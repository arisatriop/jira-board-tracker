# SMMF Board

**Internal tool for monitoring Jira boards, sprint progress, and remaining work.**

A server-rendered web dashboard that gives your team a real-time view of active sprints, ticket breakdowns, and who still has open work — all in one place, without leaving your browser.

---

## Features

- **Boards overview** — All Jira boards in one page with sprint status badges (active / overdue / no sprint) and done/total progress bars
- **Board summary modal** — Click any board to see sprint details, status/type breakdown, remaining work card, and assignee stats with tooltip
- **Remaining work page** — Full ticket list for a board's active sprint, filtered to undone only, with expandable sub-task rows
- **Client-side filters** — Search by key or summary, filter by status / type / assignee
- **Sprint timeline detection** — Boards marked overdue when sprint end date has passed
- **Internal API** — JSON endpoints under `/internal/jira/*` for programmatic access

---

## Tech Stack

- **Go 1.24** + **Fiber v2** — HTTP server and server-side HTML rendering
- **Tailwind CSS** (CDN) — Styling
- **Jira Agile REST API** — Read-only (`GET` only)

---

## Quick Start

### Prerequisites
- Go 1.24+
- PostgreSQL
- A Jira Cloud account with an API token

### Setup

```bash
# 1. Clone & install
git clone https://github.com/arisatriop/jira-board-tracker.git
cd poc-smmf-board
go mod download

# 2. Configure
cp config/config.example.yaml config/config.yaml
# Edit config/config.yaml — fill in DB credentials and Jira section

# 3. Create database
createdb poc-smmf-board

# 4. Run migrations
go run cmd/migrate/main.go

# 5. Start server
go run cmd/server/main.go
```

Server runs at `http://localhost:3000`

### Jira Configuration

In `config/config.yaml`:

```yaml
jira:
  base_url: https://<your-domain>.atlassian.net
  email: <your-email>
  api_token: <your-api-token>  # Atlassian API token (not your password)
```

Generate an API token at: **Atlassian Account Settings → Security → API tokens**

---

## Routes

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/jira/boards` | Boards overview page |
| `GET` | `/jira/boards/:id/remaining` | Remaining work detail page |
| `GET` | `/internal/jira/boards` | JSON — list all boards |
| `GET` | `/internal/jira/boards/:id/summary` | JSON — board summary |

---

## License

MIT
