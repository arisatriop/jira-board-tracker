package wire

import (
	"github.com/arisatriop/jira-board-tracker/internal/application/bar"
	"github.com/arisatriop/jira-board-tracker/internal/application/register"
	"github.com/arisatriop/jira-board-tracker/internal/bootstrap"
	"github.com/arisatriop/jira-board-tracker/internal/infrastructure/transaction"

	"gorm.io/gorm"
)

// ApplicationServices contains all application services for multi-domain orchestration
type ApplicationServices struct {
	BarSvc      bar.ApplicationService
	RegisterSvc register.ApplicationService
}

func WireApplicationServices(app *bootstrap.App, repos *Repositories, usecases *UseCases, infrastructure *Infrastructure) *ApplicationServices {
	var db *gorm.DB
	if app.DB != nil {
		db = app.DB.GDB
	}
	txManager := transaction.NewGormTransaction(db)

	return &ApplicationServices{
		BarSvc: bar.NewApplicationService(
			txManager,
			usecases.BarUC,
			repos.BarRepo,
		),
		RegisterSvc: register.NewApplicationService(
			app.Config,
			txManager,
			repos.UserRepo,
			repos.RoleRepo,
			repos.UserRoleRepo,
		),
	}
}
