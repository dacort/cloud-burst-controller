package drain

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	kubedrain "k8s.io/kubectl/pkg/drain"
)

// Drainer handles node cordon and drain operations.
type Drainer struct {
	client  kubernetes.Interface
	timeout time.Duration
}

func NewDrainer(client kubernetes.Interface, timeout time.Duration) *Drainer {
	return &Drainer{client: client, timeout: timeout}
}

func (d *Drainer) CordonNode(ctx context.Context, nodeName string) error {
	node, err := d.client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting node %s: %w", nodeName, err)
	}
	node.Spec.Unschedulable = true
	_, err = d.client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	return err
}

func (d *Drainer) DrainNode(ctx context.Context, nodeName string) error {
	helper := &kubedrain.Helper{
		Ctx:                 ctx,
		Client:              d.client,
		Force:               true,
		GracePeriodSeconds:  -1,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		Timeout:             d.timeout,
	}

	if err := kubedrain.RunNodeDrain(helper, nodeName); err != nil {
		return fmt.Errorf("draining node %s: %w", nodeName, err)
	}
	return nil
}
