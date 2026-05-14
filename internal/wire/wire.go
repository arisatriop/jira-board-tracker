package wire

import (
	"github.com/arisatriop/jira-board-tracker/internal/bootstrap"
	"github.com/arisatriop/jira-board-tracker/pkg/jira"
)

// ApplicationContainer holds all wired dependencies
type ApplicationContainer struct {
	Infrastructure      *Infrastructure
	Repositories        *Repositories
	UseCases            *UseCases
	ApplicationServices *ApplicationServices
	Handlers            *Handlers
	GrpcHandlers        *GrpcHandlers
	Middleware          *Middleware
}

// Init wires all dependencies following clean architecture layers
func Init(app *bootstrap.App) *ApplicationContainer {
	// Layer 1: Infrastructure Layer (External services, filesystem, etc.)
	infrastructure := WireInfrastructure(app)

	// Layer 2: Repository Layer (Data access)
	repositories := WireRepositories(app)

	// Layer 3: Use Case Layer (Domain/Business Logic)
	useCases := WireUseCases(app, repositories, infrastructure)

	// Layer 4: Application Service Layer (Multi-domain orchestration)
	applicationServices := WireApplicationServices(app, repositories, useCases, infrastructure)

	// Layer 5: Handler Layer (Delivery/Presentation)
	var jiraClient *jira.Client
	if app.Config.Jira.BaseURL != "" {
		jiraClient = jira.NewClient(app.Config.Jira.BaseURL, app.Config.Jira.Email, app.Config.Jira.APIToken)
	}
	handlers := WireHandlers(app, useCases, applicationServices, infrastructure, jiraClient)
	grpcHandlers := WireGrpcHandlers(useCases)

	// Layer 5: Middleware Layer
	middleware := WireMiddleware(app.Config, repositories, infrastructure)

	return &ApplicationContainer{
		Infrastructure:      infrastructure,
		Repositories:        repositories,
		UseCases:            useCases,
		ApplicationServices: applicationServices,
		Handlers:            handlers,
		GrpcHandlers:        grpcHandlers,
		Middleware:          middleware,
	}
}
