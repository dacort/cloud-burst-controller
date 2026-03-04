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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func makePodWithResources(cpu, mem string) corev1.Pod {
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test",
					Image: "busybox",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{},
					},
				},
			},
		},
	}
	if cpu != "" {
		pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse(cpu)
	}
	if mem != "" {
		pod.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse(mem)
	}
	return pod
}

func TestCalculateNodesNeeded_OnePodNoPending(t *testing.T) {
	pods := []corev1.Pod{makePodWithResources("1", "1Gi")}
	needed := CalculateNodesNeeded(pods, "m6i.large", 0, 3)
	assert.Equal(t, int32(1), needed)
}

func TestCalculateNodesNeeded_FourPodsZeroPending(t *testing.T) {
	// m6i.large: 1800m CPU, 7200Mi mem
	// Each pod: 1000m CPU, 1Gi mem → fit ~1 per node by CPU
	pods := []corev1.Pod{
		makePodWithResources("1", "1Gi"),
		makePodWithResources("1", "1Gi"),
		makePodWithResources("1", "1Gi"),
		makePodWithResources("1", "1Gi"),
	}
	needed := CalculateNodesNeeded(pods, "m6i.large", 0, 5)
	// 1800/1000 = 1 pod/node → 4 nodes needed
	assert.Equal(t, int32(4), needed)
}

func TestCalculateNodesNeeded_FourPodsOnePending(t *testing.T) {
	pods := []corev1.Pod{
		makePodWithResources("1", "1Gi"),
		makePodWithResources("1", "1Gi"),
		makePodWithResources("1", "1Gi"),
		makePodWithResources("1", "1Gi"),
	}
	// 1 node already pending, need 4 nodes for 4 pods, but only 4 slots available (5-1)
	needed := CalculateNodesNeeded(pods, "m6i.large", 1, 5)
	assert.Equal(t, int32(4), needed)
}

func TestCalculateNodesNeeded_AtMaxNodes(t *testing.T) {
	pods := []corev1.Pod{makePodWithResources("1", "1Gi")}
	needed := CalculateNodesNeeded(pods, "m6i.large", 3, 3)
	assert.Equal(t, int32(0), needed)
}

func TestCalculateNodesNeeded_CapsAtMaxAvailable(t *testing.T) {
	pods := []corev1.Pod{
		makePodWithResources("1", "1Gi"),
		makePodWithResources("1", "1Gi"),
		makePodWithResources("1", "1Gi"),
		makePodWithResources("1", "1Gi"),
		makePodWithResources("1", "1Gi"),
	}
	// Need 5 but only 2 available
	needed := CalculateNodesNeeded(pods, "m6i.large", 1, 3)
	assert.Equal(t, int32(2), needed)
}

func TestCalculateNodesNeeded_SmallPodsMultiplePerNode(t *testing.T) {
	// m6i.xlarge: 3600m CPU, 14800Mi mem
	// Each pod: 500m CPU, 512Mi mem → fit 7 by CPU, 28 by mem → 7 per node
	pods := make([]corev1.Pod, 14)
	for i := range pods {
		pods[i] = makePodWithResources("500m", "512Mi")
	}
	needed := CalculateNodesNeeded(pods, "m6i.xlarge", 0, 5)
	// 14 pods / 7 per node = 2 nodes
	assert.Equal(t, int32(2), needed)
}

func TestCalculateNodesNeeded_NoResourceRequests(t *testing.T) {
	pods := []corev1.Pod{
		makePodWithResources("", ""),
		makePodWithResources("", ""),
	}
	// No resource requests → 1 pod per node assumed
	needed := CalculateNodesNeeded(pods, "m6i.large", 0, 5)
	assert.Equal(t, int32(2), needed)
}
