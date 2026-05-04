package bar

import (
	"project-tracker/pkg/pagination"
)

type Filter struct {
	Keyword string
	Code    string

	Pagination *pagination.PaginationRequest
}
