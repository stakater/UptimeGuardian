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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UptimeProbeSpec defines the desired state of UptimeProbe
type UptimeProbeSpec struct {
	LabelSelector metav1.LabelSelector `json:"labelSelector"`
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

func init() {
	SchemeBuilder.Register(&UptimeProbe{}, &UptimeProbeList{})
}
