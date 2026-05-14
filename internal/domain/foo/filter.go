package foo

import (
	"github.com/arisatriop/jira-board-tracker/pkg/pagination"
)

type Filter struct {
	Keyword string
	Code    string

	Pagination *pagination.PaginationRequest
}
