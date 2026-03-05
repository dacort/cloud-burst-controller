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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	burstv1alpha1 "github.com/dacort/cloud-burst-controller/api/v1alpha1"
	"github.com/dacort/cloud-burst-controller/internal/cloud"
	"github.com/dacort/cloud-burst-controller/internal/drain"
)

const (
	defaultReaperCheckInterval = 60 * time.Second
	defaultCooldownPeriod      = 5 * time.Minute
	defaultDrainTimeout        = 2 * time.Minute
)

// ReaperReconciler watches burst-managed nodes and terminates them when idle.
type ReaperReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	CloudProvider cloud.Provider
	Drainer       *drain.Drainer
	CheckInterval time.Duration
}

// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=burst.homelab.dev,resources=burstnodepools,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=burst.homelab.dev,resources=burstnodepools/status,verbs=get;update;patch

func (r *ReaperReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var node corev1.Node
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Only handle burst-managed nodes
	if node.Labels["burst.homelab.dev/managed"] != labelValueTrue {
		return ctrl.Result{}, nil
	}

	// Find the pool and node status for this node
	pool, nodeIdx, err := r.findPoolAndNode(ctx, node.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if pool == nil {
		logger.V(1).Info("burst node not found in any pool status", "node", node.Name)
		return ctrl.Result{RequeueAfter: r.checkInterval()}, nil
	}

	nodeStatus := &pool.Status.Nodes[nodeIdx]

	// Only reap Running nodes
	if nodeStatus.State != burstv1alpha1.NodeStateRunning {
		return ctrl.Result{RequeueAfter: r.checkInterval()}, nil
	}

	// Check if node is idle (no non-daemonset pods)
	idle, err := r.isNodeIdle(ctx, node.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("checking if node is idle: %w", err)
	}

	if !idle {
		// Reset idle timer
		nodeStatus.LastPodFinished = nil
		if err := r.Status().Update(ctx, pool); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: r.checkInterval()}, nil
	}

	// Node is idle — track when it became idle
	now := metav1.Now()
	if nodeStatus.LastPodFinished == nil {
		logger.Info("burst node became idle", "node", node.Name, "pool", pool.Name)
		nodeStatus.LastPodFinished = &now
		if err := r.Status().Update(ctx, pool); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: r.checkInterval()}, nil
	}

	// Check cooldown
	cooldown := pool.Spec.Scaling.CooldownPeriod.Duration
	if cooldown == 0 {
		cooldown = defaultCooldownPeriod
	}
	idleDuration := time.Since(nodeStatus.LastPodFinished.Time)
	if idleDuration < cooldown {
		remaining := cooldown - idleDuration
		logger.V(1).Info("burst node idle but within cooldown", "node", node.Name, "remaining", remaining)
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	// Cooldown expired — begin teardown
	logger.Info("tearing down idle burst node", "node", node.Name, "pool", pool.Name,
		"idleDuration", idleDuration, "instanceID", nodeStatus.InstanceID)

	// Step 1: Cordon
	nodeStatus.State = burstv1alpha1.NodeStateCordoned
	if err := r.Status().Update(ctx, pool); err != nil {
		return ctrl.Result{}, err
	}
	if r.Drainer != nil {
		if err := r.Drainer.CordonNode(ctx, node.Name); err != nil {
			logger.Error(err, "failed to cordon node", "node", node.Name)
		}
	}

	// Step 2: Drain
	nodeStatus.State = burstv1alpha1.NodeStateDraining
	if err := r.Status().Update(ctx, pool); err != nil {
		return ctrl.Result{}, err
	}
	if r.Drainer != nil {
		if err := r.Drainer.DrainNode(ctx, node.Name); err != nil {
			logger.Error(err, "failed to drain node, proceeding with termination", "node", node.Name)
		}
	}

	// Step 3: Terminate cloud instance
	nodeStatus.State = burstv1alpha1.NodeStateTerminating
	if err := r.Status().Update(ctx, pool); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.CloudProvider.TerminateNode(ctx, nodeStatus.InstanceID); err != nil {
		return ctrl.Result{}, fmt.Errorf("terminating instance %s: %w", nodeStatus.InstanceID, err)
	}

	// Step 4: Delete K8s node object
	if err := r.Delete(ctx, &node); err != nil && !errors.IsNotFound(err) {
		logger.Error(err, "failed to delete node object", "node", node.Name)
	}

	// Step 5: Remove from pool status
	pool.Status.Nodes = append(pool.Status.Nodes[:nodeIdx], pool.Status.Nodes[nodeIdx+1:]...)
	pool.Status.ActiveNodes--
	if pool.Status.ActiveNodes < 0 {
		pool.Status.ActiveNodes = 0
	}
	if err := r.Status().Update(ctx, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating pool status after teardown: %w", err)
	}

	logger.Info("burst node torn down successfully", "node", node.Name, "pool", pool.Name)
	return ctrl.Result{}, nil
}

func (r *ReaperReconciler) findPoolAndNode(ctx context.Context, nodeName string) (*burstv1alpha1.BurstNodePool, int, error) {
	var poolList burstv1alpha1.BurstNodePoolList
	if err := r.List(ctx, &poolList); err != nil {
		return nil, 0, fmt.Errorf("listing pools: %w", err)
	}
	for i := range poolList.Items {
		pool := &poolList.Items[i]
		for j := range pool.Status.Nodes {
			if pool.Status.Nodes[j].Name == nodeName {
				return pool, j, nil
			}
		}
	}
	return nil, 0, nil
}

func (r *ReaperReconciler) isNodeIdle(ctx context.Context, nodeName string) (bool, error) {
	var podList corev1.PodList
	if err := r.List(ctx, &podList, client.MatchingFields{"spec.nodeName": nodeName}); err != nil {
		return false, err
	}

	for _, pod := range podList.Items {
		if isDaemonSetPod(&pod) {
			continue
		}
		// Skip completed/failed pods
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		return false, nil
	}
	return true, nil
}

func isDaemonSetPod(pod *corev1.Pod) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func (r *ReaperReconciler) checkInterval() time.Duration {
	if r.CheckInterval > 0 {
		return r.CheckInterval
	}
	return defaultReaperCheckInterval
}

func (r *ReaperReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Add field indexer for spec.nodeName so we can list pods by node
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, "spec.nodeName", func(obj client.Object) []string {
		pod, ok := obj.(*corev1.Pod)
		if !ok || pod.Spec.NodeName == "" {
			return nil
		}
		return []string{pod.Spec.NodeName}
	}); err != nil {
		return fmt.Errorf("setting up field indexer: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				if labels := e.Object.GetLabels(); labels != nil {
					return labels["burst.homelab.dev/managed"] == labelValueTrue
				}
				return false
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				if labels := e.ObjectNew.GetLabels(); labels != nil {
					return labels["burst.homelab.dev/managed"] == labelValueTrue
				}
				return false
			},
			DeleteFunc:  func(e event.DeleteEvent) bool { return false },
			GenericFunc: func(e event.GenericEvent) bool { return false },
		}).
		Named("reaper").
		Complete(r)
}
