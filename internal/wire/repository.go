package wire

import (
	"github.com/arisatriop/jira-board-tracker/internal/bootstrap"
	"github.com/arisatriop/jira-board-tracker/internal/domain/auth"
	"github.com/arisatriop/jira-board-tracker/internal/domain/bar"
	"github.com/arisatriop/jira-board-tracker/internal/domain/foo"
	"github.com/arisatriop/jira-board-tracker/internal/domain/role"
	"github.com/arisatriop/jira-board-tracker/internal/domain/user"
	"github.com/arisatriop/jira-board-tracker/internal/domain/userrole"

	"github.com/arisatriop/jira-board-tracker/internal/infrastructure/repository"

	"gorm.io/gorm"
)

// Repositories contains all repository implementations
type Repositories struct {
	AuthRepo     auth.Repository
	RoleRepo     role.Repository
	UserRepo     user.Repository
	UserRoleRepo userrole.Repository
	FooRepo      foo.Repository
	BarRepo      bar.Repository
}

// WireRepositories creates all repository implementations
func WireRepositories(app *bootstrap.App) *Repositories {
	var db *gorm.DB
	if app.DB != nil {
		db = app.DB.GDB
	}
	return &Repositories{
		AuthRepo:     repository.NewAuth(db),
		RoleRepo:     repository.NewRole(db),
		UserRepo:     repository.NewUser(db),
		UserRoleRepo: repository.NewUserRole(db),
		FooRepo:      repository.NewFoo(db),
		BarRepo:      repository.NewBar(db),
	}
}
