package wire

import (
	grpcdelivery "github.com/arisatriop/jira-board-tracker/internal/delivery/grpc"
	grpchandler "github.com/arisatriop/jira-board-tracker/internal/delivery/grpc/handler"
)

type GrpcHandlers struct {
	ServiceRegistry *grpcdelivery.ServiceRegistry
}

func WireGrpcHandlers(useCases *UseCases) *GrpcHandlers {
	hello := grpchandler.NewHello()
	foo := grpchandler.NewFoo()
	bar := grpchandler.NewBar(useCases.BarUC)

	registry := grpcdelivery.NewServiceRegistry(
		hello,
		foo,
		bar,
	)

	return &GrpcHandlers{
		ServiceRegistry: registry,
	}
}
