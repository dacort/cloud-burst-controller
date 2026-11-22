package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	burstv1alpha1 "github.com/dacort/cloud-burst-controller/api/v1alpha1"
	"github.com/dacort/cloud-burst-controller/internal/cloud"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, burstv1alpha1.AddToScheme(scheme))
	return scheme
}

func burstNode(name string, ready bool, labels map[string]string) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}},
	}
}

func poolWithNode(name string, state burstv1alpha1.NodeState) *burstv1alpha1.BurstNodePool {
	pool := &burstv1alpha1.BurstNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "pool", Namespace: "burst"},
		Spec: burstv1alpha1.BurstNodePoolSpec{
			NodeLabels: map[string]string{"workload": "batch"},
			NodeTaints: []burstv1alpha1.NodeTaint{{Key: "burst", Value: "true", Effect: "NoSchedule"}},
		},
		Status: burstv1alpha1.BurstNodePoolStatus{
			Nodes: []burstv1alpha1.BurstNodeStatus{{
				Name: name, InstanceID: "i-1", State: state, LaunchedAt: metav1.Now(),
			}},
		},
	}
	if state == burstv1alpha1.NodeStatePending {
		pool.Status.PendingNodes = 1
	} else {
		pool.Status.ActiveNodes = 1
	}
	return pool
}

func nodeReadyFixture(t *testing.T, funcs interceptor.Funcs, objs ...client.Object) *NodeReadyReconciler {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&burstv1alpha1.BurstNodePool{}).
		WithInterceptorFuncs(funcs).
		Build()
	return &NodeReadyReconciler{Client: c, Scheme: c.Scheme(), CloudProvider: &mockProvider{}}
}

func assertLabelled(t *testing.T, c client.Client, name string) {
	t.Helper()
	var node corev1.Node
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name}, &node))
	assert.Equal(t, labelValueTrue, node.Labels["burst.homelab.dev/managed"])
	assert.Equal(t, "batch", node.Labels["workload"])
	assert.Equal(t, "kept", node.Labels["other"], "labels set by others are kept")
	assert.Contains(t, node.Spec.Taints, corev1.Taint{Key: "burst", Value: "true", Effect: corev1.TaintEffectNoSchedule})
}

func TestNodeReadyLabelsTheNodeBeforeMarkingItRunning(t *testing.T) {
	ctx := context.Background()
	failPatch := true
	r := nodeReadyFixture(t, interceptor.Funcs{
		Patch: func(
			ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption,
		) error {
			if failPatch {
				return apierrors.NewConflict(corev1.Resource("nodes"), obj.GetName(), errors.New("modified"))
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
	},
		burstNode("burst-pool-a", true, map[string]string{"other": "kept"}),
		poolWithNode("burst-pool-a", burstv1alpha1.NodeStatePending),
	)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "burst-pool-a"}}

	// The label patch fails: the reconcile errors, so it's retried, and the
	// node isn't counted as running yet.
	_, err := r.Reconcile(ctx, req)
	require.Error(t, err)
	var pool burstv1alpha1.BurstNodePool
	require.NoError(t, r.Get(ctx, types.NamespacedName{Namespace: "burst", Name: "pool"}, &pool))
	assert.Equal(t, burstv1alpha1.NodeStatePending, pool.Status.Nodes[0].State)

	failPatch = false
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	assertLabelled(t, r.Client, "burst-pool-a")
	require.NoError(t, r.Get(ctx, types.NamespacedName{Namespace: "burst", Name: "pool"}, &pool))
	assert.Equal(t, burstv1alpha1.NodeStateRunning, pool.Status.Nodes[0].State)
	assert.Equal(t, int32(1), pool.Status.ActiveNodes)
	assert.Equal(t, int32(0), pool.Status.PendingNodes)
}

func TestNodeReadyRelabelsARunningNode(t *testing.T) {
	r := nodeReadyFixture(t, interceptor.Funcs{},
		burstNode("burst-pool-a", true, map[string]string{"other": "kept"}),
		poolWithNode("burst-pool-a", burstv1alpha1.NodeStateRunning),
	)
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "burst-pool-a"}})
	require.NoError(t, err)
	assertLabelled(t, r.Client, "burst-pool-a")
}

func TestOrphanDetectionMatchesInstancesToNodesByName(t *testing.T) {
	managed := map[string]string{"burst.homelab.dev/managed": labelValueTrue}
	old := time.Now().Add(-time.Hour)
	provider := &mockProvider{
		listFunc: func(ctx context.Context) ([]cloud.CloudNode, error) {
			return []cloud.CloudNode{
				// Its Node never got labelled, but it's there: keep it.
				{InstanceID: "i-unlabelled", Name: "burst-pool-unlabelled", LaunchedAt: old},
				{InstanceID: "i-labelled", Name: "burst-pool-labelled", LaunchedAt: old},
				// No Node past the boot timeout: terminate it.
				{InstanceID: "i-nonode", Name: "burst-pool-nonode", LaunchedAt: old},
			}, nil
		},
	}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(
		burstNode("burst-pool-unlabelled", true, nil),
		burstNode("burst-pool-labelled", true, managed),
		// No instance behind them:
		burstNode("burst-pool-gone", true, managed), // labelled: delete
		burstNode("burst-pool-stale", false, nil),   // unlabelled, NotReady: delete
		burstNode("burst-someone-elses", true, nil), // unlabelled, Ready: leave
		burstNode("homelab-1", false, nil),          // not a burst node: leave
	).Build()
	r := &OrphanReconciler{Client: c, Scheme: c.Scheme(), CloudProvider: provider}

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	require.NoError(t, err)
	assert.Equal(t, []string{"i-nonode"}, provider.terminated)

	var nodes corev1.NodeList
	require.NoError(t, c.List(context.Background(), &nodes))
	var names []string
	for _, n := range nodes.Items {
		names = append(names, n.Name)
	}
	assert.ElementsMatch(t,
		[]string{"burst-pool-unlabelled", "burst-pool-labelled", "burst-someone-elses", "homelab-1"}, names)
}
