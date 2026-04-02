package resources

import "context"

// Resource is the interface all AWS resources must implement
type Resource interface {
	ID() string
	Name() string
	Status() string
	Region() string
	Columns() []string
	Values() []string
}

// Provider fetches resources of a specific type
type Provider interface {
	Kind() string
	Fetch(ctx context.Context, region string) ([]Resource, error)
	Headers() []string
}
