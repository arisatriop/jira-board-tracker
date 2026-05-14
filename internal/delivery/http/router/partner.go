package router

import (
	"github.com/arisatriop/jira-board-tracker/internal/bootstrap"
	"github.com/arisatriop/jira-board-tracker/internal/wire"

	"github.com/gofiber/fiber/v2"
)

type PartnerRouteRegistry struct {
	App   *bootstrap.App
	Wired *wire.ApplicationContainer
}

func (r *PartnerRouteRegistry) register(route fiber.Router) {
	partner := route.Group("partner").Use(r.Wired.Middleware.Auth.PartnerAuthenticate(), r.Wired.Middleware.RateLimit.Partner)
	v1 := partner.Group("v1")

	r.foo(v1)
	r.bar(v1)
	r.jira(v1)
}

func (r *PartnerRouteRegistry) foo(v1 fiber.Router) {
	foo := v1.Group("foos")
	foo.Post("",
		r.Wired.Handlers.Foo.Create)

	foo.Put("/:id",
		r.Wired.Handlers.Foo.Update)

	foo.Delete("/:id",
		r.Wired.Handlers.Foo.Delete)

	foo.Get("",
		r.Wired.Handlers.Foo.List)

	foo.Get("/:id",
		r.Wired.Handlers.Foo.Get)
}

func (r *PartnerRouteRegistry) jira(v1 fiber.Router) {
	jira := v1.Group("jira")
	jira.Get("/boards", r.Wired.Handlers.Jira.GetBoards)
	jira.Get("/boards/:id/summary", r.Wired.Handlers.Jira.GetBoardSummary)
	jira.Get("/boards/:id/story-points", r.Wired.Handlers.Jira.GetBoardStoryPoints)
}

func (r *PartnerRouteRegistry) bar(v1 fiber.Router) {
	bar := v1.Group("bars")
	bar.Post("",
		r.Wired.Handlers.Bar.Create)

	bar.Put("/:id",
		r.Wired.Handlers.Bar.Update)

	bar.Delete("/:id",
		r.Wired.Handlers.Bar.Delete)

	bar.Get("",
		r.Wired.Handlers.Bar.List)

	bar.Get("/:id",
		r.Wired.Handlers.Bar.Get)
}
