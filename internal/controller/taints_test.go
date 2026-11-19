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

	burstv1alpha1 "github.com/dacort/cloud-burst-controller/api/v1alpha1"
)

func TestTalosTaints(t *testing.T) {
	assert.Nil(t, talosTaints(nil))
	assert.Equal(t, map[string]string{"burst.homelab.dev/cloud": "true:NoSchedule", "spot": ":NoExecute"},
		talosTaints([]burstv1alpha1.NodeTaint{
			{Key: "burst.homelab.dev/cloud", Value: "true", Effect: "NoSchedule"},
			{Key: "spot", Effect: "NoExecute"},
		}))
}

// withTaints adds the pool's taints once, keeping the node's own.
func TestWithTaints(t *testing.T) {
	pool := []burstv1alpha1.NodeTaint{{Key: "burst.homelab.dev/cloud", Value: "true", Effect: "NoSchedule"}}
	own := corev1.Taint{Key: "node.kubernetes.io/not-ready", Effect: corev1.TaintEffectNoSchedule}
	want := corev1.Taint{Key: "burst.homelab.dev/cloud", Value: "true", Effect: corev1.TaintEffectNoSchedule}

	got := withTaints([]corev1.Taint{own}, pool)
	assert.Equal(t, []corev1.Taint{own, want}, got)
	// Already there (registered from the Talos config): unchanged.
	assert.Equal(t, got, withTaints(got, pool))
}
