/*
Copyright 2021.

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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TODO: Add resource limits
// RedisClusterSpec defines the desired state of RedisCluster
type RedisClusterSpec struct {
	Auth       RedisAuth           `json:"auth,omitempty"`
	Version    string              `json:"version,omitempty"`
	Replicas   int32               `json:"replicas,omitempty"`
	Image      string              `json:"image,omitempty"`
	Storage    string              `json:"storage,omitempty"`
	Monitoring *v1.PodTemplateSpec `json:"monitoring,omitempty"`
	Config     string              `json:"config,omitempty"`
}

// RedisClusterStatus defines the observed state of RedisCluster
type RedisClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Nodes []RedisNode `json:"nodes"`
}

type RedisNode struct {
	NodeName string `json:"name"`
	IP       string `json:"ip"`
}

// RedisAuth
type RedisAuth struct {
	SecretName string `json:"secret,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// RedisCluster is the Schema for the redisclusters API
type RedisCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              RedisClusterSpec   `json:"spec,omitempty"`
	Status            RedisClusterStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// RedisClusterList contains a list of RedisCluster
type RedisClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RedisCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RedisCluster{}, &RedisClusterList{})
}
