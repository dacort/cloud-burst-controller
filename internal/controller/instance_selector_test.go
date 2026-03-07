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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	burstv1alpha1 "github.com/dacort/cloud-burst-controller/api/v1alpha1"
)

func makePodWithGPU(cpu, mem string, gpus int) corev1.Pod {
	pod := makePodWithResources(cpu, mem)
	if gpus > 0 {
		pod.Spec.Containers[0].Resources.Requests[corev1.ResourceName("nvidia.com/gpu")] =
			*resource.NewQuantity(int64(gpus), resource.DecimalSI)
	}
	return pod
}

func TestSelectInstanceTypes_SingleType_BackwardCompat(t *testing.T) {
	pool := &burstv1alpha1.BurstNodePool{
		Spec: burstv1alpha1.BurstNodePoolSpec{
			AWS: &burstv1alpha1.AWSConfig{
				InstanceType: "t3.large",
			},
		},
	}
	pods := []corev1.Pod{makePodWithResources("100m", "256Mi")}
	result := SelectInstanceTypes(pool, pods)
	require.Len(t, result, 1)
	assert.Equal(t, "t3.large", result[0].Name)
}

func TestSelectInstanceTypes_MultipleTypes_SmallestFitFirst(t *testing.T) {
	pool := &burstv1alpha1.BurstNodePool{
		Spec: burstv1alpha1.BurstNodePoolSpec{
			AWS: &burstv1alpha1.AWSConfig{
				InstanceTypes: []burstv1alpha1.InstanceTypeConfig{
					{Name: "t3.large"},    // 1800m CPU, 7200Mi
					{Name: "m6i.xlarge"},  // 3600m CPU, 14800Mi
					{Name: "m6i.2xlarge"}, // 7200m CPU, 29600Mi
				},
			},
		},
	}

	// Pod needs 3000m CPU — t3.large (1800m) can't fit, m6i.xlarge (3600m) is smallest fit.
	pods := []corev1.Pod{makePodWithResources("3000m", "1Gi")}
	result := SelectInstanceTypes(pool, pods)

	require.Len(t, result, 2)
	assert.Equal(t, "m6i.xlarge", result[0].Name)
	assert.Equal(t, "m6i.2xlarge", result[1].Name)
}

func TestSelectInstanceTypes_MemoryHeavyPod(t *testing.T) {
	pool := &burstv1alpha1.BurstNodePool{
		Spec: burstv1alpha1.BurstNodePoolSpec{
			AWS: &burstv1alpha1.AWSConfig{
				InstanceTypes: []burstv1alpha1.InstanceTypeConfig{
					{Name: "t3.large"},   // 1800m, 7200Mi
					{Name: "m6i.xlarge"}, // 3600m, 14800Mi
					{Name: "r6i.xlarge"}, // 3600m, 29600Mi
				},
			},
		},
	}

	// Pod needs 20Gi (~20480Mi) memory — only r6i.xlarge (29600Mi) fits.
	pods := []corev1.Pod{makePodWithResources("1000m", "20Gi")}
	result := SelectInstanceTypes(pool, pods)

	require.Len(t, result, 1)
	assert.Equal(t, "r6i.xlarge", result[0].Name)
}

func TestSelectInstanceTypes_GPUPod(t *testing.T) {
	pool := &burstv1alpha1.BurstNodePool{
		Spec: burstv1alpha1.BurstNodePoolSpec{
			AWS: &burstv1alpha1.AWSConfig{
				InstanceTypes: []burstv1alpha1.InstanceTypeConfig{
					{Name: "m6i.xlarge"}, // no GPU
					{Name: "g5.xlarge"},  // 1 GPU
					{Name: "g5.2xlarge"}, // 1 GPU
				},
			},
		},
	}

	pods := []corev1.Pod{makePodWithGPU("1000m", "4Gi", 1)}
	result := SelectInstanceTypes(pool, pods)

	require.Len(t, result, 2)
	assert.Equal(t, "g5.xlarge", result[0].Name)
	assert.Equal(t, "g5.2xlarge", result[1].Name)
}

func TestSelectInstanceTypes_Arm64Affinity(t *testing.T) {
	pool := &burstv1alpha1.BurstNodePool{
		Spec: burstv1alpha1.BurstNodePoolSpec{
			AWS: &burstv1alpha1.AWSConfig{
				InstanceTypes: []burstv1alpha1.InstanceTypeConfig{
					{Name: "m6i.large"}, // amd64
					{Name: "m7g.large"}, // arm64
					{Name: "m7g.xlarge"},
				},
			},
		},
	}

	pod := makePodWithResources("500m", "1Gi")
	pod.Spec.Affinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      "kubernetes.io/arch",
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"arm64"},
							},
						},
					},
				},
			},
		},
	}

	result := SelectInstanceTypes(pool, []corev1.Pod{pod})
	require.Len(t, result, 2)
	for _, r := range result {
		cap, ok := GetInstanceCapacity(r.Name)
		require.True(t, ok)
		assert.Equal(t, "arm64", cap.Architecture, "expected arm64, got %s for %s", cap.Architecture, r.Name)
	}
}

func TestSelectInstanceTypes_NoCandidates(t *testing.T) {
	pool := &burstv1alpha1.BurstNodePool{
		Spec: burstv1alpha1.BurstNodePoolSpec{
			AWS: &burstv1alpha1.AWSConfig{
				InstanceTypes: []burstv1alpha1.InstanceTypeConfig{
					{Name: "t3.medium"}, // 1800m, 3500Mi
				},
			},
		},
	}

	// Pod needs 64Gi — nothing fits.
	pods := []corev1.Pod{makePodWithResources("1000m", "64Gi")}
	result := SelectInstanceTypes(pool, pods)
	assert.Empty(t, result)
}

func TestSelectInstanceTypes_InstanceTypesOverridesInstanceType(t *testing.T) {
	pool := &burstv1alpha1.BurstNodePool{
		Spec: burstv1alpha1.BurstNodePoolSpec{
			AWS: &burstv1alpha1.AWSConfig{
				InstanceType: "t3.medium",
				InstanceTypes: []burstv1alpha1.InstanceTypeConfig{
					{Name: "m6i.large"},
				},
			},
		},
	}
	pods := []corev1.Pod{makePodWithResources("500m", "1Gi")}
	result := SelectInstanceTypes(pool, pods)
	require.Len(t, result, 1)
	assert.Equal(t, "m6i.large", result[0].Name)
}

func TestSelectInstanceTypes_AMIOverridePreserved(t *testing.T) {
	pool := &burstv1alpha1.BurstNodePool{
		Spec: burstv1alpha1.BurstNodePoolSpec{
			AWS: &burstv1alpha1.AWSConfig{
				AMI: "ami-default",
				InstanceTypes: []burstv1alpha1.InstanceTypeConfig{
					{Name: "g5.xlarge", AMI: "ami-gpu-special"},
				},
			},
		},
	}
	pods := []corev1.Pod{makePodWithGPU("1000m", "4Gi", 1)}
	result := SelectInstanceTypes(pool, pods)
	require.Len(t, result, 1)
	assert.Equal(t, "ami-gpu-special", result[0].AMI)
}

func TestSelectInstanceTypes_NilAWS(t *testing.T) {
	pool := &burstv1alpha1.BurstNodePool{
		Spec: burstv1alpha1.BurstNodePoolSpec{},
	}
	result := SelectInstanceTypes(pool, []corev1.Pod{makePodWithResources("1", "1Gi")})
	assert.Nil(t, result)
}
