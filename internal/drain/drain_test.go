package drain

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCordonNode(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "burst-node-1"},
		Spec:       corev1.NodeSpec{Unschedulable: false},
	}
	client := fake.NewClientset(node)
	drainer := NewDrainer(client, 30*time.Second)

	err := drainer.CordonNode(context.Background(), "burst-node-1")
	require.NoError(t, err)

	updated, err := client.CoreV1().Nodes().Get(context.Background(), "burst-node-1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.True(t, updated.Spec.Unschedulable)
}

func TestCordonNode_NotFound(t *testing.T) {
	client := fake.NewClientset()
	drainer := NewDrainer(client, 30*time.Second)

	err := drainer.CordonNode(context.Background(), "nonexistent")
	assert.Error(t, err)
}

func TestDrainNode_NoPods(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "burst-node-1"},
	}
	client := fake.NewClientset(node)
	drainer := NewDrainer(client, 30*time.Second)

	err := drainer.DrainNode(context.Background(), "burst-node-1")
	require.NoError(t, err)
}

// A node running a DaemonSet pod makes the drain helper print a warning
// ("ignoring DaemonSet-managed Pods"); with no writer set, that print
// panicked and the teardown stopped before terminating the instance.
func TestDrainNodeWithDaemonSetPod(t *testing.T) {
	isController := true
	ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "kube-proxy", Namespace: "kube-system"}}
	dsPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kube-proxy-abcde", Namespace: "kube-system",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "DaemonSet", Name: "kube-proxy", Controller: &isController,
			}},
		},
		Spec: corev1.PodSpec{NodeName: "burst-node-1"},
	}
	work := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "work", Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "burst-node-1"},
	}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "burst-node-1"}}
	client := fake.NewClientset(node, ds, dsPod, work)
	// Discovery with no eviction subresource, so the helper deletes.
	client.Resources = []*metav1.APIResourceList{{GroupVersion: "v1"}}

	d := NewDrainer(client, 10*time.Second)
	require.NotPanics(t, func() {
		require.NoError(t, d.DrainNode(context.Background(), "burst-node-1"))
	})

	_, err := client.CoreV1().Pods("default").Get(context.Background(), "work", metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "the workload pod should be gone, got %v", err)
	_, err = client.CoreV1().Pods("kube-system").Get(context.Background(), "kube-proxy-abcde", metav1.GetOptions{})
	assert.NoError(t, err, "the DaemonSet pod should be left alone")
}
