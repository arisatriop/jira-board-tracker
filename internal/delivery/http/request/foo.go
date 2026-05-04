package request

import (
	dtorequest "project-tracker/internal/delivery/http/dto/request"
	"project-tracker/internal/domain/foo"

	"github.com/gofiber/fiber/v2"
)

func ToFooFilter(req *dtorequest.FooListRequest, ctx *fiber.Ctx) *foo.Filter {
	panic("Implement me")
}
