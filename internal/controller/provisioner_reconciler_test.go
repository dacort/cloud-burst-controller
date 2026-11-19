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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	burstv1alpha1 "github.com/dacort/cloud-burst-controller/api/v1alpha1"
	"github.com/dacort/cloud-burst-controller/internal/cloud"
)

const testWorkerConfig = `machine:
  type: worker
  network:
    hostname: ""
cluster:
  controlPlane:
    endpoint: https://10.0.0.1:6443
`

// provisionerFixture is a pool, its Talos config secret, and one pending
// pod that matches it, in a fake API server.
func provisionerFixture(t *testing.T, provider cloud.Provider) (*ProvisionerReconciler, types.NamespacedName) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, burstv1alpha1.AddToScheme(scheme))

	pool := &burstv1alpha1.BurstNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pool", Namespace: "burst"},
		Spec: burstv1alpha1.BurstNodePoolSpec{
			Cloud: "aws",
			AWS: &burstv1alpha1.AWSConfig{
				Region:        "us-west-2",
				AMI:           "ami-test",
				InstanceTypes: []burstv1alpha1.InstanceTypeConfig{{Name: "m6i.large"}},
			},
			Talos:      burstv1alpha1.TalosConfig{MachineConfigSecret: "worker-config", MachineConfigKey: "worker.yaml"},
			Scaling:    burstv1alpha1.ScalingConfig{MaxNodes: 1},
			MatchRules: burstv1alpha1.MatchRules{Tolerations: []burstv1alpha1.TolerationRule{{Key: "burst", Operator: "Exists"}}},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-config", Namespace: "burst"},
		Data:       map[string][]byte{"worker.yaml": []byte(testWorkerConfig)},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "work", Namespace: "default",
			Labels: map[string]string{"burst.homelab.dev/enabled": "true"},
		},
		Spec: corev1.PodSpec{
			Tolerations: []corev1.Toleration{{Key: "burst", Operator: corev1.TolerationOpExists}},
			Containers:  []corev1.Container{{Name: "c", Image: "busybox"}},
		},
		Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
			Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable,
			LastTransitionTime: metav1.Now(),
		}}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(pool, secret, pod).
		WithStatusSubresource(&burstv1alpha1.BurstNodePool{}).
		Build()
	return &ProvisionerReconciler{Client: c, Scheme: scheme, CloudProvider: provider},
		types.NamespacedName{Namespace: "default", Name: "work"}
}

// A launch that fails leaves the pod unmarked and retries it later; the
// next one that succeeds marks it.
func TestProvisionerRetriesAFailedLaunch(t *testing.T) {
	ctx := context.Background()
	fail := true
	provider := &mockProvider{launchFunc: func(ctx context.Context, opts cloud.LaunchOptions) (string, error) {
		if fail {
			return "", errors.New("InvalidParameterValue: something EC2 refused")
		}
		return "i-launched", nil
	}}
	r, podKey := provisionerFixture(t, provider)
	req := ctrl.Request{NamespacedName: podKey}

	res, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, launchRetryBase, res.RequeueAfter)
	var pod corev1.Pod
	require.NoError(t, r.Get(ctx, podKey, &pod))
	assert.NotContains(t, pod.Annotations, annotationProvisioned)
	assert.Equal(t, "1", pod.Annotations[annotationLaunchFailures])

	res, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 2*launchRetryBase, res.RequeueAfter, "the backoff grows")

	fail = false
	res, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Zero(t, res.RequeueAfter)
	require.NoError(t, r.Get(ctx, podKey, &pod))
	assert.Equal(t, labelValueTrue, pod.Annotations[annotationProvisioned])
	assert.NotContains(t, pod.Annotations, annotationLaunchFailures)

	var pool burstv1alpha1.BurstNodePool
	require.NoError(t, r.Get(ctx, types.NamespacedName{Namespace: "burst", Name: "test-pool"}, &pool))
	require.Len(t, pool.Status.Nodes, 1)
	assert.Equal(t, "i-launched", pool.Status.Nodes[0].InstanceID)
	assert.Equal(t, int32(1), pool.Status.PendingNodes)
}

func TestLaunchRetryBackoff(t *testing.T) {
	assert.Equal(t, 30*time.Second, launchRetryBackoff(1))
	assert.Equal(t, 60*time.Second, launchRetryBackoff(2))
	assert.Equal(t, 4*time.Minute, launchRetryBackoff(4))
	assert.Equal(t, 10*time.Minute, launchRetryBackoff(6))
	assert.Equal(t, 10*time.Minute, launchRetryBackoff(100))
}
