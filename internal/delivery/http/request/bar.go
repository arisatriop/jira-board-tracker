package request

import (
	dtorequest "project-tracker/internal/delivery/http/dto/request"
	"project-tracker/internal/domain/bar"
	"project-tracker/pkg/pagination"

	"github.com/gofiber/fiber/v2"
)

func ToBarFilter(req *dtorequest.BarListRequest, ctx *fiber.Ctx) *bar.Filter {
	filter := &bar.Filter{
		Keyword:    req.Keyword,
		Pagination: pagination.ParsePagination(ctx),
	}

	return filter
}
