package request

import (
	dtorequest "github.com/arisatriop/jira-board-tracker/internal/delivery/http/dto/request"
	"github.com/arisatriop/jira-board-tracker/internal/domain/bar"
	"github.com/arisatriop/jira-board-tracker/pkg/pagination"

	"github.com/gofiber/fiber/v2"
)

func ToBarFilter(req *dtorequest.BarListRequest, ctx *fiber.Ctx) *bar.Filter {
	filter := &bar.Filter{
		Keyword:    req.Keyword,
		Pagination: pagination.ParsePagination(ctx),
	}

	return filter
}
