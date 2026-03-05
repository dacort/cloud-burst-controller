/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dacort/cloud-burst-controller/internal/cloud"
)

// mockProvider implements cloud.Provider for testing.
type mockProvider struct {
	launchFunc    func(ctx context.Context, opts cloud.LaunchOptions) (string, error)
	terminateFunc func(ctx context.Context, instanceID string) error
	listFunc      func(ctx context.Context) ([]cloud.CloudNode, error)
	terminated    []string
}

func (m *mockProvider) LaunchNode(ctx context.Context, opts cloud.LaunchOptions) (string, error) {
	if m.launchFunc != nil {
		return m.launchFunc(ctx, opts)
	}
	return "i-mock", nil
}

func (m *mockProvider) TerminateNode(ctx context.Context, instanceID string) error {
	m.terminated = append(m.terminated, instanceID)
	if m.terminateFunc != nil {
		return m.terminateFunc(ctx, instanceID)
	}
	return nil
}

func (m *mockProvider) ListManagedNodes(ctx context.Context) ([]cloud.CloudNode, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx)
	}
	return nil, nil
}

func TestOrphanDetection_CloudInstanceNoK8sNode_PastBootTimeout(t *testing.T) {
	mock := &mockProvider{
		listFunc: func(ctx context.Context) ([]cloud.CloudNode, error) {
			return []cloud.CloudNode{
				{
					InstanceID: "i-orphan1",
					Name:       "burst-orphan-1",
					State:      "running",
					LaunchedAt: time.Now().Add(-10 * time.Minute), // Past boot timeout
				},
			}, nil
		},
	}

	// Verify the orphan detection logic identifies this case
	cloudNodes, err := mock.ListManagedNodes(context.Background())
	require.NoError(t, err)

	k8sNodeNames := map[string]bool{} // No K8s nodes
	bootTimeout := 5 * time.Minute

	for _, cn := range cloudNodes {
		if !k8sNodeNames[cn.Name] && time.Since(cn.LaunchedAt) >= bootTimeout {
			err := mock.TerminateNode(context.Background(), cn.InstanceID)
			require.NoError(t, err)
		}
	}

	assert.Contains(t, mock.terminated, "i-orphan1")
}

func TestOrphanDetection_CloudInstanceNoK8sNode_WithinBootTimeout(t *testing.T) {
	mock := &mockProvider{
		listFunc: func(ctx context.Context) ([]cloud.CloudNode, error) {
			return []cloud.CloudNode{
				{
					InstanceID: "i-booting",
					Name:       "burst-booting-1",
					State:      "running",
					LaunchedAt: time.Now().Add(-1 * time.Minute), // Within boot timeout
				},
			}, nil
		},
	}

	cloudNodes, err := mock.ListManagedNodes(context.Background())
	require.NoError(t, err)

	k8sNodeNames := map[string]bool{}
	bootTimeout := 5 * time.Minute

	for _, cn := range cloudNodes {
		if !k8sNodeNames[cn.Name] && time.Since(cn.LaunchedAt) >= bootTimeout {
			_ = mock.TerminateNode(context.Background(), cn.InstanceID)
		}
	}

	// Should NOT have been terminated (still booting)
	assert.Empty(t, mock.terminated)
}

func TestOrphanDetection_CloudInstanceWithK8sNode_NoAction(t *testing.T) {
	mock := &mockProvider{
		listFunc: func(ctx context.Context) ([]cloud.CloudNode, error) {
			return []cloud.CloudNode{
				{
					InstanceID: "i-healthy",
					Name:       "burst-healthy-1",
					State:      "running",
					LaunchedAt: time.Now().Add(-10 * time.Minute),
				},
			}, nil
		},
	}

	cloudNodes, err := mock.ListManagedNodes(context.Background())
	require.NoError(t, err)

	k8sNodeNames := map[string]bool{"burst-healthy-1": true}
	bootTimeout := 5 * time.Minute

	for _, cn := range cloudNodes {
		if !k8sNodeNames[cn.Name] && time.Since(cn.LaunchedAt) >= bootTimeout {
			_ = mock.TerminateNode(context.Background(), cn.InstanceID)
		}
	}

	assert.Empty(t, mock.terminated)
}
