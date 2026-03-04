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

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// NodesActive tracks the current number of active burst nodes per pool.
	NodesActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "burst_nodes_active",
			Help: "Number of active burst nodes",
		},
		[]string{"pool"},
	)

	// NodesProvisionedTotal counts total nodes provisioned per pool.
	NodesProvisionedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "burst_nodes_provisioned_total",
			Help: "Total number of burst nodes provisioned",
		},
		[]string{"pool"},
	)

	// NodesTerminatedTotal counts total nodes terminated per pool.
	NodesTerminatedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "burst_nodes_terminated_total",
			Help: "Total number of burst nodes terminated",
		},
		[]string{"pool"},
	)

	// ProvisionDurationSeconds tracks how long it takes for a burst node to become ready.
	ProvisionDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "burst_provision_duration_seconds",
			Help:    "Time from launch to node ready",
			Buckets: prometheus.ExponentialBuckets(15, 2, 8), // 15s, 30s, 1m, 2m, 4m, 8m, 16m, 32m
		},
		[]string{"pool"},
	)

	// ProvisionFailuresTotal counts provision failures per pool.
	ProvisionFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "burst_provision_failures_total",
			Help: "Total number of burst node provision failures",
		},
		[]string{"pool"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		NodesActive,
		NodesProvisionedTotal,
		NodesTerminatedTotal,
		ProvisionDurationSeconds,
		ProvisionFailuresTotal,
	)
}
