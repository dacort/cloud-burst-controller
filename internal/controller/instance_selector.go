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
	"sort"

	corev1 "k8s.io/api/core/v1"

	burstv1alpha1 "github.com/dacort/cloud-burst-controller/api/v1alpha1"
)

// SelectInstanceTypes picks instance types from the pool's list that can fit the
// pending pods' resource requirements, sorted smallest-fit-first.
// Returns nil if no candidates can satisfy the requirements.
func SelectInstanceTypes(pool *burstv1alpha1.BurstNodePool, pods []corev1.Pod) []burstv1alpha1.InstanceTypeConfig {
	if pool.Spec.AWS == nil {
		return nil
	}

	candidates := pool.Spec.AWS.ResolveInstanceTypes()
	if len(candidates) == 0 {
		return nil
	}

	maxCPU, maxMem, maxGPU := maxPodRequirements(pods)
	requiredArch := requiredArchitecture(pods)
	needsGPU := maxGPU > 0

	var fits []burstv1alpha1.InstanceTypeConfig
	for _, candidate := range candidates {
		cap, ok := instanceCapacities[candidate.Name]
		if !ok {
			// Unknown instance type — include it anyway (we can't filter it).
			// The launch will succeed or fail on its own.
			fits = append(fits, candidate)
			continue
		}

		// Architecture filter.
		arch := candidate.Architecture
		if arch == "" {
			arch = cap.Architecture
		}
		if requiredArch != "" && arch != requiredArch {
			continue
		}

		// GPU filter.
		if needsGPU && int64(cap.GPUs) < maxGPU {
			continue
		}

		// CPU/memory filter.
		if cap.VCPUs < maxCPU || cap.MemoryMi < maxMem {
			continue
		}

		fits = append(fits, candidate)
	}

	// Sort by total capacity ascending (smallest that fits first).
	sort.Slice(fits, func(i, j int) bool {
		ci, oki := instanceCapacities[fits[i].Name]
		cj, okj := instanceCapacities[fits[j].Name]
		if !oki || !okj {
			return oki // known types before unknown
		}
		if ci.VCPUs != cj.VCPUs {
			return ci.VCPUs < cj.VCPUs
		}
		return ci.MemoryMi < cj.MemoryMi
	})

	return fits
}

// maxPodRequirements computes the maximum CPU (milli), memory (MiB), and GPU
// requests across the given pods.
func maxPodRequirements(pods []corev1.Pod) (cpuMilli int64, memMi int64, gpus int64) {
	for _, pod := range pods {
		cpu, mem := podResourceRequests(&pod)
		gpu := podGPURequests(&pod)
		if cpu > cpuMilli {
			cpuMilli = cpu
		}
		if mem > memMi {
			memMi = mem
		}
		if gpu > gpus {
			gpus = gpu
		}
	}
	return
}

// podGPURequests returns the total nvidia.com/gpu requests for a pod.
func podGPURequests(pod *corev1.Pod) int64 {
	var gpus int64
	for _, c := range pod.Spec.Containers {
		if gpu, ok := c.Resources.Requests[corev1.ResourceName("nvidia.com/gpu")]; ok {
			gpus += gpu.Value()
		}
	}
	return gpus
}

// requiredArchitecture inspects pod node affinity for kubernetes.io/arch constraints.
// Returns the required architecture or "" if unconstrained.
func requiredArchitecture(pods []corev1.Pod) string {
	for _, pod := range pods {
		if pod.Spec.Affinity == nil || pod.Spec.Affinity.NodeAffinity == nil {
			continue
		}
		required := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
		if required == nil {
			continue
		}
		for _, term := range required.NodeSelectorTerms {
			for _, expr := range term.MatchExpressions {
				if expr.Key == "kubernetes.io/arch" && expr.Operator == corev1.NodeSelectorOpIn && len(expr.Values) > 0 {
					return expr.Values[0]
				}
			}
		}
	}
	return ""
}

// PodsRequestGPU returns true if any pod requests nvidia.com/gpu resources.
func PodsRequestGPU(pods []corev1.Pod) bool {
	for _, pod := range pods {
		if podGPURequests(&pod) > 0 {
			return true
		}
	}
	return false
}
