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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BurstNodePoolSpec defines the desired state of BurstNodePool.
type BurstNodePoolSpec struct {
	// Cloud provider discriminator. Only "aws" is supported in v1.
	// +kubebuilder:validation:Enum=aws
	Cloud string `json:"cloud"`

	// AWS-specific configuration. Required when cloud is "aws".
	// +optional
	AWS *AWSConfig `json:"aws,omitempty"`

	// Talos machine config reference.
	Talos TalosConfig `json:"talos"`

	// Scaling parameters.
	Scaling ScalingConfig `json:"scaling"`

	// Rules for matching unschedulable pods to this pool.
	MatchRules MatchRules `json:"matchRules"`

	// Labels applied to burst nodes when they register.
	// +optional
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// Taints applied to burst nodes when they register.
	// +optional
	NodeTaints []NodeTaint `json:"nodeTaints,omitempty"`
}

type AWSConfig struct {
	// AWS region for EC2 instances.
	Region string `json:"region"`

	// AMI ID for Talos instances.
	AMI string `json:"ami"`

	// EC2 instance type.
	InstanceType string `json:"instanceType"`

	// Subnet ID for instance placement.
	SubnetID string `json:"subnetId"`

	// Security group IDs.
	SecurityGroupIDs []string `json:"securityGroupIds"`

	// Root volume size in GB.
	// +kubebuilder:default=30
	VolumeSize int32 `json:"volumeSize,omitempty"`

	// Root volume type.
	// +kubebuilder:default=gp3
	VolumeType string `json:"volumeType,omitempty"`

	// Additional EC2 tags.
	// +optional
	Tags map[string]string `json:"tags,omitempty"`
}

type TalosConfig struct {
	// Name of the Secret containing the Talos machine config.
	MachineConfigSecret string `json:"machineConfigSecret"`

	// Key within the Secret.
	// +kubebuilder:default=worker.yaml
	MachineConfigKey string `json:"machineConfigKey,omitempty"`
}

type ScalingConfig struct {
	// Maximum number of nodes this pool can provision.
	// +kubebuilder:validation:Minimum=0
	MaxNodes int32 `json:"maxNodes"`

	// How long a node must be idle before termination.
	// +kubebuilder:default="5m"
	CooldownPeriod metav1.Duration `json:"cooldownPeriod,omitempty"`

	// Max time to wait for a node to become Ready.
	// +kubebuilder:default="5m"
	BootTimeout metav1.Duration `json:"bootTimeout,omitempty"`
}

type MatchRules struct {
	// Tolerations the pod must have.
	// +optional
	Tolerations []TolerationRule `json:"tolerations,omitempty"`

	// Node affinity labels the pod must request.
	// +optional
	NodeAffinityLabels map[string]string `json:"nodeAffinityLabels,omitempty"`

	// Resource requests the pod must have.
	// +optional
	Resources []ResourceRule `json:"resources,omitempty"`
}

type TolerationRule struct {
	Key      string `json:"key"`
	Operator string `json:"operator"`
	// +optional
	Value string `json:"value,omitempty"`
}

type ResourceRule struct {
	ResourceName string `json:"resourceName"`
	Operator     string `json:"operator"`
}

type NodeTaint struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Effect string `json:"effect"`
}

// BurstNodePoolStatus defines the observed state of BurstNodePool.
type BurstNodePoolStatus struct {
	// Number of nodes in Running state.
	ActiveNodes int32 `json:"activeNodes"`

	// Number of nodes in Pending state.
	PendingNodes int32 `json:"pendingNodes"`

	// Individual node statuses.
	// +optional
	Nodes []BurstNodeStatus `json:"nodes,omitempty"`

	// Standard conditions.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:validation:Enum=Pending;Running;Cordoned;Draining;Terminating
type NodeState string

const (
	NodeStatePending     NodeState = "Pending"
	NodeStateRunning     NodeState = "Running"
	NodeStateCordoned    NodeState = "Cordoned"
	NodeStateDraining    NodeState = "Draining"
	NodeStateTerminating NodeState = "Terminating"
)

type BurstNodeStatus struct {
	// Kubernetes node name.
	Name string `json:"name"`

	// Cloud provider instance ID.
	InstanceID string `json:"instanceId"`

	// Current lifecycle state.
	State NodeState `json:"state"`

	// When the instance was launched.
	LaunchedAt metav1.Time `json:"launchedAt"`

	// When the node became Ready.
	// +optional
	ReadyAt *metav1.Time `json:"readyAt,omitempty"`

	// When the last non-daemonset pod finished on this node.
	// +optional
	LastPodFinished *metav1.Time `json:"lastPodFinished,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Cloud",type=string,JSONPath=`.spec.cloud`
// +kubebuilder:printcolumn:name="Max",type=integer,JSONPath=`.spec.scaling.maxNodes`
// +kubebuilder:printcolumn:name="Active",type=integer,JSONPath=`.status.activeNodes`
// +kubebuilder:printcolumn:name="Pending",type=integer,JSONPath=`.status.pendingNodes`

// BurstNodePool is the Schema for the burstnodepools API.
type BurstNodePool struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec BurstNodePoolSpec `json:"spec"`

	// +optional
	Status BurstNodePoolStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BurstNodePoolList contains a list of BurstNodePool.
type BurstNodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []BurstNodePool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BurstNodePool{}, &BurstNodePoolList{})
}
