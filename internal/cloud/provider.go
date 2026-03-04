package cloud

import "context"

// Provider abstracts cloud instance operations.
type Provider interface {
	// LaunchNode provisions a new cloud instance and returns its instance ID.
	LaunchNode(ctx context.Context, opts LaunchOptions) (string, error)

	// TerminateNode terminates a cloud instance by ID.
	TerminateNode(ctx context.Context, instanceID string) error

	// ListManagedNodes returns all instances tagged as burst-managed.
	ListManagedNodes(ctx context.Context) ([]CloudNode, error)
}
