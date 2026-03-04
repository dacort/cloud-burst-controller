package drain

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCordonNode(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "burst-node-1"},
		Spec:       corev1.NodeSpec{Unschedulable: false},
	}
	client := fake.NewSimpleClientset(node)
	drainer := NewDrainer(client, 30*time.Second)

	err := drainer.CordonNode(context.Background(), "burst-node-1")
	require.NoError(t, err)

	updated, err := client.CoreV1().Nodes().Get(context.Background(), "burst-node-1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.True(t, updated.Spec.Unschedulable)
}

func TestCordonNode_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	drainer := NewDrainer(client, 30*time.Second)

	err := drainer.CordonNode(context.Background(), "nonexistent")
	assert.Error(t, err)
}

func TestDrainNode_NoPods(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "burst-node-1"},
	}
	client := fake.NewSimpleClientset(node)
	drainer := NewDrainer(client, 30*time.Second)

	err := drainer.DrainNode(context.Background(), "burst-node-1")
	require.NoError(t, err)
}
