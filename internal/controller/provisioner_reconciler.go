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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	burstv1alpha1 "github.com/dacort/cloud-burst-controller/api/v1alpha1"
	"github.com/dacort/cloud-burst-controller/internal/cloud"
	"github.com/dacort/cloud-burst-controller/internal/talos"
)

const (
	defaultDebouncePeriod = 30 * time.Second
)

// ProvisionerReconciler watches unschedulable Pods and provisions burst nodes.
type ProvisionerReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	CloudProvider  cloud.Provider
	DebouncePeriod time.Duration
}

// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=burst.homelab.dev,resources=burstnodepools,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=burst.homelab.dev,resources=burstnodepools/status,verbs=get;update;patch

func (r *ProvisionerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the pod
	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Only handle unschedulable pods
	if !IsPodUnschedulable(&pod) {
		return ctrl.Result{}, nil
	}

	// Debounce: unless burst is explicitly enabled, wait before acting
	if !IsBurstEnabled(&pod) {
		unschedulableSince := podUnschedulableSince(&pod)
		if unschedulableSince.IsZero() || time.Since(unschedulableSince) < r.debouncePeriod() {
			logger.V(1).Info("pod within debounce period, requeueing", "pod", req.NamespacedName)
			return ctrl.Result{RequeueAfter: r.debouncePeriod()}, nil
		}
	}

	// List all BurstNodePools
	var poolList burstv1alpha1.BurstNodePoolList
	if err := r.List(ctx, &poolList); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing BurstNodePools: %w", err)
	}

	// Match pod to a pool
	pool := MatchPodToPool(&pod, poolList.Items)
	if pool == nil {
		logger.V(1).Info("no matching pool for unschedulable pod", "pod", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	logger.Info("matched unschedulable pod to pool", "pod", req.NamespacedName, "pool", pool.Name)

	// Check capacity
	currentNodes := pool.Status.ActiveNodes + pool.Status.PendingNodes
	if currentNodes >= pool.Spec.Scaling.MaxNodes {
		logger.Info("pool at max capacity", "pool", pool.Name, "current", currentNodes, "max", pool.Spec.Scaling.MaxNodes)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Collect all unschedulable pods matching this pool for batch sizing
	var podList corev1.PodList
	if err := r.List(ctx, &podList); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing pods: %w", err)
	}
	var matchingPods []corev1.Pod
	for _, p := range podList.Items {
		if IsPodUnschedulable(&p) && MatchPodToPool(&p, []burstv1alpha1.BurstNodePool{*pool}) != nil {
			matchingPods = append(matchingPods, p)
		}
	}

	instanceType := pool.Spec.AWS.InstanceType
	nodesNeeded := CalculateNodesNeeded(matchingPods, instanceType, currentNodes, pool.Spec.Scaling.MaxNodes)
	if nodesNeeded <= 0 {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	logger.Info("provisioning burst nodes", "pool", pool.Name, "count", nodesNeeded)

	// Read Talos config secret
	var secret corev1.Secret
	secretKey := types.NamespacedName{
		Namespace: pool.Namespace,
		Name:      pool.Spec.Talos.MachineConfigSecret,
	}
	if err := r.Get(ctx, secretKey, &secret); err != nil {
		return ctrl.Result{}, fmt.Errorf("getting Talos config secret %s: %w", secretKey, err)
	}
	configKey := pool.Spec.Talos.MachineConfigKey
	if configKey == "" {
		configKey = "worker.yaml"
	}
	configData, ok := secret.Data[configKey]
	if !ok {
		return ctrl.Result{}, fmt.Errorf("key %q not found in secret %s", configKey, secretKey)
	}

	// Launch nodes
	for i := int32(0); i < nodesNeeded; i++ {
		nodeName := fmt.Sprintf("burst-%s-%s", pool.Name, randomSuffix())

		userData, err := talos.PatchAndEncode(configData, nodeName)
		if err != nil {
			logger.Error(err, "failed to patch Talos config", "nodeName", nodeName)
			continue
		}

		opts := cloud.LaunchOptions{
			Name:             nodeName,
			AMI:              pool.Spec.AWS.AMI,
			InstanceType:     instanceType,
			SubnetID:         pool.Spec.AWS.SubnetID,
			SecurityGroupIDs: pool.Spec.AWS.SecurityGroupIDs,
			VolumeSize:       pool.Spec.AWS.VolumeSize,
			VolumeType:       pool.Spec.AWS.VolumeType,
			UserData:         userData,
			Tags:             pool.Spec.AWS.Tags,
		}

		instanceID, err := r.CloudProvider.LaunchNode(ctx, opts)
		if err != nil {
			logger.Error(err, "failed to launch burst node", "pool", pool.Name, "nodeName", nodeName)
			continue
		}

		logger.Info("launched burst node", "pool", pool.Name, "nodeName", nodeName, "instanceID", instanceID)

		// Update pool status with pending node
		pool.Status.Nodes = append(pool.Status.Nodes, burstv1alpha1.BurstNodeStatus{
			Name:       nodeName,
			InstanceID: instanceID,
			State:      burstv1alpha1.NodeStatePending,
			LaunchedAt: metav1.Now(),
		})
		pool.Status.PendingNodes++
	}

	// Persist status update
	if err := r.Status().Update(ctx, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating pool status: %w", err)
	}

	// Requeue to check if nodes become ready
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *ProvisionerReconciler) debouncePeriod() time.Duration {
	if r.DebouncePeriod > 0 {
		return r.DebouncePeriod
	}
	return defaultDebouncePeriod
}

// SetupWithManager sets up the provisioner controller.
func (r *ProvisionerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool { return true },
			UpdateFunc: func(e event.UpdateEvent) bool {
				// Only reconcile if pod conditions changed
				oldPod, ok1 := e.ObjectOld.(*corev1.Pod)
				newPod, ok2 := e.ObjectNew.(*corev1.Pod)
				if !ok1 || !ok2 {
					return false
				}
				return IsPodUnschedulable(newPod) && !IsPodUnschedulable(oldPod)
			},
			DeleteFunc:  func(e event.DeleteEvent) bool { return false },
			GenericFunc: func(e event.GenericEvent) bool { return false },
		}).
		Named("provisioner").
		Complete(r)
}

// NodeReadyReconciler watches nodes and updates BurstNodePool status when burst nodes become ready.
type NodeReadyReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	CloudProvider cloud.Provider
}

// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;update;patch;delete

func (r *NodeReadyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var node corev1.Node
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Only handle burst-managed nodes
	if node.Labels["burst.homelab.dev/managed"] != "true" {
		return ctrl.Result{}, nil
	}

	// Find the pool for this node
	var poolList burstv1alpha1.BurstNodePoolList
	if err := r.List(ctx, &poolList); err != nil {
		return ctrl.Result{}, err
	}

	for i := range poolList.Items {
		pool := &poolList.Items[i]
		for j := range pool.Status.Nodes {
			nodeStatus := &pool.Status.Nodes[j]
			if nodeStatus.Name != node.Name {
				continue
			}

			if nodeStatus.State == burstv1alpha1.NodeStatePending && isNodeReady(&node) {
				logger.Info("burst node became ready", "node", node.Name, "pool", pool.Name)
				now := metav1.Now()
				nodeStatus.State = burstv1alpha1.NodeStateRunning
				nodeStatus.ReadyAt = &now
				pool.Status.PendingNodes--
				pool.Status.ActiveNodes++

				// Apply node labels from pool spec
				if node.Labels == nil {
					node.Labels = make(map[string]string)
				}
				for k, v := range pool.Spec.NodeLabels {
					node.Labels[k] = v
				}
				if err := r.Update(ctx, &node); err != nil {
					logger.Error(err, "failed to update node labels", "node", node.Name)
				}

				if err := r.Status().Update(ctx, pool); err != nil {
					return ctrl.Result{}, fmt.Errorf("updating pool status: %w", err)
				}
				return ctrl.Result{}, nil
			}

			// Check boot timeout
			if nodeStatus.State == burstv1alpha1.NodeStatePending {
				bootTimeout := pool.Spec.Scaling.BootTimeout.Duration
				if bootTimeout == 0 {
					bootTimeout = 5 * time.Minute
				}
				if time.Since(nodeStatus.LaunchedAt.Time) > bootTimeout {
					logger.Info("burst node exceeded boot timeout, terminating",
						"node", node.Name, "pool", pool.Name, "instanceID", nodeStatus.InstanceID)

					if err := r.CloudProvider.TerminateNode(ctx, nodeStatus.InstanceID); err != nil {
						logger.Error(err, "failed to terminate timed-out node", "instanceID", nodeStatus.InstanceID)
					}

					// Remove from pool status
					pool.Status.Nodes = append(pool.Status.Nodes[:j], pool.Status.Nodes[j+1:]...)
					pool.Status.PendingNodes--
					if err := r.Status().Update(ctx, pool); err != nil {
						return ctrl.Result{}, fmt.Errorf("updating pool status: %w", err)
					}
					return ctrl.Result{}, nil
				}

				// Requeue to check again
				return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *NodeReadyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc:  func(e event.CreateEvent) bool { return true },
			UpdateFunc:  func(e event.UpdateEvent) bool { return true },
			DeleteFunc:  func(e event.DeleteEvent) bool { return false },
			GenericFunc: func(e event.GenericEvent) bool { return false },
		}).
		Named("nodeready").
		Complete(r)
}

func isNodeReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func podUnschedulableSince(pod *corev1.Pod) time.Time {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled &&
			cond.Status == corev1.ConditionFalse &&
			cond.Reason == corev1.PodReasonUnschedulable {
			return cond.LastTransitionTime.Time
		}
	}
	return time.Time{}
}

func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// MapNodeToPodReconcile creates a handler that maps node events to pod reconcile requests.
func MapNodeToPodReconcile(ctx context.Context, obj client.Object) []reconcile.Request {
	// Not used directly - nodes are watched by NodeReadyReconciler
	return nil
}
