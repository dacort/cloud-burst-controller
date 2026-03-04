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

// MatchPodToPool finds the first BurstNodePool that matches a pod's tolerations
// and node affinity labels. Pools are sorted alphabetically by name for determinism.
// Returns nil if no pool matches.
func MatchPodToPool(pod *corev1.Pod, pools []burstv1alpha1.BurstNodePool) *burstv1alpha1.BurstNodePool {
	// Sort pools alphabetically for deterministic matching
	sorted := make([]burstv1alpha1.BurstNodePool, len(pools))
	copy(sorted, pools)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	for i := range sorted {
		if podMatchesPool(pod, &sorted[i]) {
			return &sorted[i]
		}
	}
	return nil
}

// podMatchesPool checks whether a pod satisfies a pool's match rules.
func podMatchesPool(pod *corev1.Pod, pool *burstv1alpha1.BurstNodePool) bool {
	rules := pool.Spec.MatchRules

	// Check tolerations: pod must have all required tolerations
	for _, required := range rules.Tolerations {
		if !podHasToleration(pod, required) {
			return false
		}
	}

	// Check node affinity labels: pod must request all specified labels
	if len(rules.NodeAffinityLabels) > 0 {
		if !podHasNodeAffinityLabels(pod, rules.NodeAffinityLabels) {
			return false
		}
	}

	return true
}

// podHasToleration checks if a pod has a toleration matching the rule.
func podHasToleration(pod *corev1.Pod, rule burstv1alpha1.TolerationRule) bool {
	for _, t := range pod.Spec.Tolerations {
		if t.Key == rule.Key {
			switch rule.Operator {
			case "Exists":
				return true
			case "Equal":
				if t.Value == rule.Value {
					return true
				}
			}
		}
	}
	return false
}

// podHasNodeAffinityLabels checks if a pod's node affinity requires the specified labels.
func podHasNodeAffinityLabels(pod *corev1.Pod, labels map[string]string) bool {
	affinity := pod.Spec.Affinity
	if affinity == nil || affinity.NodeAffinity == nil {
		return false
	}
	required := affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if required == nil {
		return false
	}

	// Check if any node selector term matches all required labels
	for _, term := range required.NodeSelectorTerms {
		if termMatchesLabels(term, labels) {
			return true
		}
	}
	return false
}

// termMatchesLabels checks if a NodeSelectorTerm has match expressions for all given labels.
func termMatchesLabels(term corev1.NodeSelectorTerm, labels map[string]string) bool {
	for key, value := range labels {
		found := false
		for _, expr := range term.MatchExpressions {
			if expr.Key == key && expr.Operator == corev1.NodeSelectorOpIn {
				for _, v := range expr.Values {
					if v == value {
						found = true
						break
					}
				}
			}
			// Also match Exists operator (no value check needed)
			if expr.Key == key && expr.Operator == corev1.NodeSelectorOpExists {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// IsBurstEnabled returns true if the pod has the burst.homelab.dev/enabled=true label,
// which bypasses the debounce period.
func IsBurstEnabled(pod *corev1.Pod) bool {
	return pod.Labels["burst.homelab.dev/enabled"] == "true"
}

// IsPodUnschedulable returns true if the pod has PodScheduled=False with reason Unschedulable.
func IsPodUnschedulable(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled &&
			cond.Status == corev1.ConditionFalse &&
			cond.Reason == corev1.PodReasonUnschedulable {
			return true
		}
	}
	return false
}
