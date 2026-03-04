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
	"math"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// InstanceCapacity describes the allocatable resources of an instance type.
type InstanceCapacity struct {
	VCPUs    int64 // millicores
	MemoryMi int64 // mebibytes
}

// Known instance type capacities (allocatable, roughly 90% of total).
var instanceCapacities = map[string]InstanceCapacity{
	"t3.medium":   {VCPUs: 1800, MemoryMi: 3500},
	"t3.large":    {VCPUs: 1800, MemoryMi: 7200},
	"t3.xlarge":   {VCPUs: 3600, MemoryMi: 14800},
	"m6i.large":   {VCPUs: 1800, MemoryMi: 7200},
	"m6i.xlarge":  {VCPUs: 3600, MemoryMi: 14800},
	"m6i.2xlarge": {VCPUs: 7200, MemoryMi: 29600},
	"m7i.large":   {VCPUs: 1800, MemoryMi: 7200},
	"m7i.xlarge":  {VCPUs: 3600, MemoryMi: 14800},
	"c6i.large":   {VCPUs: 1800, MemoryMi: 3500},
	"c6i.xlarge":  {VCPUs: 3600, MemoryMi: 7200},
	"r6i.large":   {VCPUs: 1800, MemoryMi: 14800},
	"r6i.xlarge":  {VCPUs: 3600, MemoryMi: 29600},
}

// defaultCapacity is used when instance type is not in the table.
var defaultCapacity = InstanceCapacity{VCPUs: 1800, MemoryMi: 7200}

// CalculateNodesNeeded determines how many new nodes to launch given:
// - pending pods that need scheduling
// - the instance type to use
// - current active + pending node count
// - max nodes allowed
func CalculateNodesNeeded(pods []corev1.Pod, instanceType string, currentNodes, maxNodes int32) int32 {
	if currentNodes >= maxNodes {
		return 0
	}

	capacity, ok := instanceCapacities[instanceType]
	if !ok {
		capacity = defaultCapacity
	}

	podsPerNode := estimatePodsPerNode(pods, capacity)
	if podsPerNode <= 0 {
		podsPerNode = 1
	}

	nodesNeeded := int32(math.Ceil(float64(len(pods)) / float64(podsPerNode)))
	available := maxNodes - currentNodes
	if nodesNeeded > available {
		nodesNeeded = available
	}
	if nodesNeeded < 0 {
		nodesNeeded = 0
	}

	return nodesNeeded
}

// estimatePodsPerNode estimates how many of the given pods can fit on one node.
// Uses the maximum resource request across all pods as the representative size.
func estimatePodsPerNode(pods []corev1.Pod, capacity InstanceCapacity) int32 {
	if len(pods) == 0 {
		return 0
	}

	var maxCPU, maxMem int64
	for _, pod := range pods {
		cpu, mem := podResourceRequests(&pod)
		if cpu > maxCPU {
			maxCPU = cpu
		}
		if mem > maxMem {
			maxMem = mem
		}
	}

	// If no resource requests, assume 1 pod per node
	if maxCPU == 0 && maxMem == 0 {
		return 1
	}

	fitByCPU := int32(math.MaxInt32)
	fitByMem := int32(math.MaxInt32)

	if maxCPU > 0 {
		fitByCPU = int32(capacity.VCPUs / maxCPU)
	}
	if maxMem > 0 {
		fitByMem = int32(capacity.MemoryMi / maxMem)
	}

	fit := fitByCPU
	if fitByMem < fit {
		fit = fitByMem
	}
	if fit <= 0 {
		fit = 1
	}
	return fit
}

// podResourceRequests returns the total CPU (millicores) and memory (MiB) requests for a pod.
func podResourceRequests(pod *corev1.Pod) (cpuMilli int64, memMi int64) {
	for _, c := range pod.Spec.Containers {
		if cpu, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
			cpuMilli += cpu.MilliValue()
		}
		if mem, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
			memMi += mem.ScaledValue(resource.Mega)
		}
	}
	return
}
