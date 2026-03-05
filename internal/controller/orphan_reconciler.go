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
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	burstv1alpha1 "github.com/dacort/cloud-burst-controller/api/v1alpha1"
	"github.com/dacort/cloud-burst-controller/internal/cloud"
)

const (
	defaultOrphanCheckInterval = 5 * time.Minute
	defaultBootTimeoutOrphan   = 5 * time.Minute
)

// OrphanReconciler periodically compares cloud instances against K8s nodes
// and cleans up orphans in either direction.
type OrphanReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	CloudProvider cloud.Provider
	CheckInterval time.Duration
	BootTimeout   time.Duration
}

func (r *OrphanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.V(1).Info("running orphan detection")

	// Get all cloud instances tagged as managed
	cloudNodes, err := r.CloudProvider.ListManagedNodes(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("listing managed cloud nodes: %w", err)
	}

	// Get all K8s nodes with burst label
	var nodeList corev1.NodeList
	if err := r.List(ctx, &nodeList, client.MatchingLabels{"burst.homelab.dev/managed": labelValueTrue}); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing burst K8s nodes: %w", err)
	}

	// Build lookup maps
	k8sNodeNames := make(map[string]bool)
	for _, node := range nodeList.Items {
		k8sNodeNames[node.Name] = true
	}

	cloudInstancesByName := make(map[string]cloud.CloudNode)
	for _, cn := range cloudNodes {
		cloudInstancesByName[cn.Name] = cn
	}

	bootTimeout := r.bootTimeout()

	// Case 1: Cloud instance exists, no K8s node, launched > bootTimeout ago → terminate
	for _, cn := range cloudNodes {
		if k8sNodeNames[cn.Name] {
			continue // Has a matching K8s node
		}
		if time.Since(cn.LaunchedAt) < bootTimeout {
			logger.V(1).Info("cloud instance without K8s node, still within boot timeout",
				"instance", cn.InstanceID, "name", cn.Name)
			continue
		}
		logger.Info("terminating orphaned cloud instance (no K8s node)",
			"instance", cn.InstanceID, "name", cn.Name, "age", time.Since(cn.LaunchedAt))
		if err := r.CloudProvider.TerminateNode(ctx, cn.InstanceID); err != nil {
			logger.Error(err, "failed to terminate orphaned instance", "instance", cn.InstanceID)
		}
		// Clean from pool status if present
		r.removeFromPoolStatus(ctx, cn.Name)
	}

	// Case 2: K8s node exists, no cloud instance → delete K8s node
	for _, node := range nodeList.Items {
		if _, exists := cloudInstancesByName[node.Name]; exists {
			continue // Has a matching cloud instance
		}
		logger.Info("deleting orphaned K8s node (no cloud instance)", "node", node.Name)
		if err := r.Delete(ctx, &node); err != nil && !errors.IsNotFound(err) {
			logger.Error(err, "failed to delete orphaned K8s node", "node", node.Name)
		}
		// Clean from pool status
		r.removeFromPoolStatus(ctx, node.Name)
	}

	return ctrl.Result{RequeueAfter: r.checkInterval()}, nil
}

func (r *OrphanReconciler) removeFromPoolStatus(ctx context.Context, nodeName string) {
	logger := log.FromContext(ctx)

	var poolList burstv1alpha1.BurstNodePoolList
	if err := r.List(ctx, &poolList); err != nil {
		logger.Error(err, "failed to list pools for orphan cleanup")
		return
	}

	for i := range poolList.Items {
		pool := &poolList.Items[i]
		for j := range pool.Status.Nodes {
			if pool.Status.Nodes[j].Name == nodeName {
				state := pool.Status.Nodes[j].State
				pool.Status.Nodes = append(pool.Status.Nodes[:j], pool.Status.Nodes[j+1:]...)
				switch state {
				case burstv1alpha1.NodeStatePending:
					pool.Status.PendingNodes--
				case burstv1alpha1.NodeStateRunning, burstv1alpha1.NodeStateCordoned, burstv1alpha1.NodeStateDraining:
					pool.Status.ActiveNodes--
				}
				if pool.Status.ActiveNodes < 0 {
					pool.Status.ActiveNodes = 0
				}
				if pool.Status.PendingNodes < 0 {
					pool.Status.PendingNodes = 0
				}
				if err := r.Status().Update(ctx, pool); err != nil {
					logger.Error(err, "failed to update pool status during orphan cleanup", "pool", pool.Name)
				}
				return
			}
		}
	}
}

func (r *OrphanReconciler) checkInterval() time.Duration {
	if r.CheckInterval > 0 {
		return r.CheckInterval
	}
	return defaultOrphanCheckInterval
}

func (r *OrphanReconciler) bootTimeout() time.Duration {
	if r.BootTimeout > 0 {
		return r.BootTimeout
	}
	return defaultBootTimeoutOrphan
}

// SetupWithManager registers the orphan reconciler as a runnable that triggers periodically.
// It uses a dummy reconciler that requeues itself.
func (r *OrphanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return mgr.Add(&orphanRunner{reconciler: r, interval: r.checkInterval()})
}

// orphanRunner implements manager.Runnable for periodic orphan checks.
type orphanRunner struct {
	reconciler *OrphanReconciler
	interval   time.Duration
}

func (o *orphanRunner) Start(ctx context.Context) error {
	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()

	// Run immediately on start
	_, _ = o.reconciler.Reconcile(ctx, reconcile.Request{})

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := o.reconciler.Reconcile(ctx, reconcile.Request{}); err != nil {
				log.FromContext(ctx).Error(err, "orphan reconciler error")
			}
		}
	}
}
