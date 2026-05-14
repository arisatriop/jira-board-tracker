package presenter

import (
	dtoresponse "github.com/arisatriop/jira-board-tracker/internal/delivery/http/dto/response"
	"github.com/arisatriop/jira-board-tracker/internal/domain/foo"
)

// ToFooResponse converts a single foo entity to DTO
func ToFooResponse(entity *foo.Foo) *dtoresponse.FooResponse {
	panic("Implement me")
}

// ToFooListResponse converts multiple bar entities to DTOs
func ToFooListResponse(entities []*foo.Foo) []*dtoresponse.FooResponse {
	panic("Implement me")
}
