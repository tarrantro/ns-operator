package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
type NamespaceClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              NamespaceClassSpec   `json:"spec,omitempty"`
	Status            NamespaceClassStatus `json:"status,omitempty"`
}

type NamespaceClassSpec struct {
	// Resources is a list of arbitrary resource manifests that the controller
	// creates in any namespace labeled with this NamespaceClass.
	// +kubebuilder:pruning:PreserveUnknownFields
	Resources []runtime.RawExtension `json:"resources,omitempty"`
}

type NamespaceClassStatus struct {
	// ObservedKinds accumulates every resource Kind this NamespaceClass has
	// ever declared in Spec.Resources.
	// +listType=map
	// +listMapKey=apiVersion
	// +listMapKey=kind
	ObservedKinds []ObservedKind `json:"observedKinds,omitempty"`
}

type ObservedKind struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type NamespaceClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NamespaceClass `json:"items"`
}
