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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	burstv1alpha1 "github.com/dacort/cloud-burst-controller/api/v1alpha1"
)

// A teardown that stopped partway (here, after the drain) is finished by
// the next reconcile instead of being skipped because the node is no
// longer Running.
func TestReaperResumesAnInterruptedTeardown(t *testing.T) {
	for _, state := range []burstv1alpha1.NodeState{
		burstv1alpha1.NodeStateCordoned,
		burstv1alpha1.NodeStateDraining,
		burstv1alpha1.NodeStateTerminating,
	} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, burstv1alpha1.AddToScheme(scheme))

			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name:   "burst-pool-abc",
				Labels: map[string]string{"burst.homelab.dev/managed": "true"},
			}}
			pool := &burstv1alpha1.BurstNodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "pool", Namespace: "burst"},
				Status: burstv1alpha1.BurstNodePoolStatus{
					ActiveNodes: 1,
					Nodes: []burstv1alpha1.BurstNodeStatus{{
						Name: "burst-pool-abc", InstanceID: "i-stuck", State: state,
					}},
				},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(node, pool).
				WithStatusSubresource(&burstv1alpha1.BurstNodePool{}).
				Build()
			provider := &mockProvider{}
			r := &ReaperReconciler{Client: c, Scheme: scheme, CloudProvider: provider}

			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "burst-pool-abc"}})
			require.NoError(t, err)

			assert.Equal(t, []string{"i-stuck"}, provider.terminated)
			err = c.Get(ctx, types.NamespacedName{Name: "burst-pool-abc"}, &corev1.Node{})
			assert.True(t, apierrors.IsNotFound(err), "node object should be deleted, got %v", err)
			var got burstv1alpha1.BurstNodePool
			require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: "burst", Name: "pool"}, &got))
			assert.Empty(t, got.Status.Nodes)
			assert.Zero(t, got.Status.ActiveNodes)
		})
	}
}
