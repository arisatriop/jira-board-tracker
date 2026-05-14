package request

import (
	dtorequest "github.com/arisatriop/jira-board-tracker/internal/delivery/http/dto/request"
	"github.com/arisatriop/jira-board-tracker/internal/domain/foo"

	"github.com/gofiber/fiber/v2"
)

func ToFooFilter(req *dtorequest.FooListRequest, ctx *fiber.Ctx) *foo.Filter {
	panic("Implement me")
}
