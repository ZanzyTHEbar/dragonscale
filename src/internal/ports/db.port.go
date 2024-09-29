package ports

import "context"

// Generic interface for database operations
type DBPort[T any] interface {
	Create(ctx context.Context, entity *T) (*T, error)
	GetById(ctx context.Context, id int) (*T, error)
	GetAll(ctx context.Context) ([]*T, error)
	Update(ctx context.Context, entity *T) (*T, error)
	Delete(ctx context.Context, id int) error
}
