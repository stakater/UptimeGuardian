/*
Copyright 2025.

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
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProberConfig defines the configuration for the prober service
type ProberConfig struct {
	// URL is the URL of the prober service
	// +kubebuilder:default="blackbox-exporter.monitoring-system.svc:19115"
	URL string `json:"url,omitempty"`

	// Scheme is the scheme to use for the prober (http/https)
	// +kubebuilder:default="http"
	Scheme string `json:"scheme,omitempty"`

	// Path is the path to use for the prober
	// +kubebuilder:default="/probe"
	Path string `json:"path,omitempty"`

	// Module is the prober module to use
	// +kubebuilder:default="http_2xx"
	Module string `json:"module,omitempty"`

	// JobName is the name of the job
	// +kubebuilder:default="http_get"
	JobName string `json:"jobName,omitempty"`

	// Interval is the interval between probe requests
	// +kubebuilder:default="30s"
	Interval monitoringv1.Duration `json:"interval,omitempty"`
}

// UptimeProbeSpec defines the desired state of UptimeProbe
type UptimeProbeSpec struct {
	// LabelSelector for filtering routes. If empty, no filtering will be applied.
	// +optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`

	// ProberConfig contains the configuration for the prober service
	ProberConfig ProberConfig `json:"proberConfig"`

	// TargetNamespace is the namespace where Probe resources will be created
	// +kubebuilder:default="test-1"
	TargetNamespace string `json:"targetNamespace,omitempty"`
}

// UptimeProbeStatus defines the observed state of UptimeProbe
type UptimeProbeStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// UptimeProbe is the Schema for the uptimeprobes API
type UptimeProbe struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UptimeProbeSpec   `json:"spec,omitempty"`
	Status UptimeProbeStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// UptimeProbeList contains a list of UptimeProbe
type UptimeProbeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []UptimeProbe `json:"items"`
}

// DefaultUptimeProbe returns a singleton UptimeProbe instance with default values
func DefaultUptimeProbe() *UptimeProbe {
	return &UptimeProbe{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-uptime-probe",
			Namespace: "test-1",
		},
		Spec: UptimeProbeSpec{
			ProberConfig: ProberConfig{
				URL:      "blackbox-exporter.monitoring-system.svc:19115",
				Scheme:   "http",
				Path:     "/probe",
				Module:   "http_2xx",
				JobName:  "http_get",
				Interval: monitoringv1.Duration("30s"),
			},
			TargetNamespace: "test-1",
		},
	}
}

func init() {
	SchemeBuilder.Register(&UptimeProbe{}, &UptimeProbeList{})
}
