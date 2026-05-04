package wire

import (
	"project-tracker/internal/bootstrap"
	"project-tracker/internal/domain/auth"
	"project-tracker/internal/domain/bar"
	"project-tracker/internal/domain/foo"
	"project-tracker/internal/domain/role"
	"project-tracker/internal/domain/user"
	"project-tracker/internal/domain/userrole"

	"project-tracker/internal/infrastructure/repository"
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
	db := app.DB.GDB
	return &Repositories{
		AuthRepo:     repository.NewAuth(db),
		RoleRepo:     repository.NewRole(db),
		UserRepo:     repository.NewUser(db),
		UserRoleRepo: repository.NewUserRole(db),
		FooRepo:      repository.NewFoo(db),
		BarRepo:      repository.NewBar(db),
	}
}
