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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	burstv1alpha1 "github.com/dacort/cloud-burst-controller/api/v1alpha1"
)

func makePool(name string, tolerations []burstv1alpha1.TolerationRule, affinityLabels map[string]string) burstv1alpha1.BurstNodePool {
	return burstv1alpha1.BurstNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: burstv1alpha1.BurstNodePoolSpec{
			MatchRules: burstv1alpha1.MatchRules{
				Tolerations:        tolerations,
				NodeAffinityLabels: affinityLabels,
			},
		},
	}
}

func TestMatchPodToPool_MatchingToleration(t *testing.T) {
	pool := makePool("general-burst", []burstv1alpha1.TolerationRule{
		{Key: "burst.homelab.dev/cloud", Operator: "Exists"},
	}, nil)

	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Tolerations: []corev1.Toleration{
				{Key: "burst.homelab.dev/cloud", Operator: corev1.TolerationOpExists},
			},
		},
	}

	result := MatchPodToPool(pod, []burstv1alpha1.BurstNodePool{pool})
	require.NotNil(t, result)
	assert.Equal(t, "general-burst", result.Name)
}

func TestMatchPodToPool_NoToleration(t *testing.T) {
	pool := makePool("general-burst", []burstv1alpha1.TolerationRule{
		{Key: "burst.homelab.dev/cloud", Operator: "Exists"},
	}, nil)

	pod := &corev1.Pod{
		Spec: corev1.PodSpec{},
	}

	result := MatchPodToPool(pod, []burstv1alpha1.BurstNodePool{pool})
	assert.Nil(t, result)
}

func TestMatchPodToPool_WithNodeAffinityLabels(t *testing.T) {
	pool := makePool("gpu-burst", []burstv1alpha1.TolerationRule{
		{Key: "burst.homelab.dev/cloud", Operator: "Exists"},
	}, map[string]string{
		"burst.homelab.dev/pool": "gpu-burst",
	})

	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Tolerations: []corev1.Toleration{
				{Key: "burst.homelab.dev/cloud", Operator: corev1.TolerationOpExists},
			},
			Affinity: &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      "burst.homelab.dev/pool",
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"gpu-burst"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	result := MatchPodToPool(pod, []burstv1alpha1.BurstNodePool{pool})
	require.NotNil(t, result)
	assert.Equal(t, "gpu-burst", result.Name)
}

func TestMatchPodToPool_MultiplePoolsFirstAlphabeticalWins(t *testing.T) {
	poolA := makePool("alpha-burst", []burstv1alpha1.TolerationRule{
		{Key: "burst.homelab.dev/cloud", Operator: "Exists"},
	}, nil)
	poolB := makePool("beta-burst", []burstv1alpha1.TolerationRule{
		{Key: "burst.homelab.dev/cloud", Operator: "Exists"},
	}, nil)

	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Tolerations: []corev1.Toleration{
				{Key: "burst.homelab.dev/cloud", Operator: corev1.TolerationOpExists},
			},
		},
	}

	// Pass in reverse order to verify sorting
	result := MatchPodToPool(pod, []burstv1alpha1.BurstNodePool{poolB, poolA})
	require.NotNil(t, result)
	assert.Equal(t, "alpha-burst", result.Name)
}

func TestMatchPodToPool_ResourceExists(t *testing.T) {
	pool := burstv1alpha1.BurstNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-pool"},
		Spec: burstv1alpha1.BurstNodePoolSpec{
			MatchRules: burstv1alpha1.MatchRules{
				Resources: []burstv1alpha1.ResourceRule{
					{ResourceName: "nvidia.com/gpu", Operator: "Exists"},
				},
			},
		},
	}

	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
						},
					},
				},
			},
		},
	}

	result := MatchPodToPool(pod, []burstv1alpha1.BurstNodePool{pool})
	require.NotNil(t, result)
	assert.Equal(t, "gpu-pool", result.Name)
}

func TestMatchPodToPool_ResourceDoesNotExist(t *testing.T) {
	pool := burstv1alpha1.BurstNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "cpu-pool"},
		Spec: burstv1alpha1.BurstNodePoolSpec{
			MatchRules: burstv1alpha1.MatchRules{
				Resources: []burstv1alpha1.ResourceRule{
					{ResourceName: "nvidia.com/gpu", Operator: "DoesNotExist"},
				},
			},
		},
	}

	// Pod WITHOUT GPU
	podNoGPU := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1"),
						},
					},
				},
			},
		},
	}
	result := MatchPodToPool(podNoGPU, []burstv1alpha1.BurstNodePool{pool})
	require.NotNil(t, result)

	// Pod WITH GPU should NOT match
	podGPU := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
						},
					},
				},
			},
		},
	}
	result = MatchPodToPool(podGPU, []burstv1alpha1.BurstNodePool{pool})
	assert.Nil(t, result)
}

func TestIsBurstEnabled(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"burst.homelab.dev/enabled": "true"},
		},
	}
	assert.True(t, IsBurstEnabled(pod))

	pod2 := &corev1.Pod{}
	assert.False(t, IsBurstEnabled(pod2))
}

func TestIsPodUnschedulable(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodScheduled,
					Status: corev1.ConditionFalse,
					Reason: corev1.PodReasonUnschedulable,
				},
			},
		},
	}
	assert.True(t, IsPodUnschedulable(pod))

	pod2 := &corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodScheduled,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	assert.False(t, IsPodUnschedulable(pod2))
}
