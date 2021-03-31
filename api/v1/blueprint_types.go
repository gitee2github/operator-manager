/*


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

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	StepStatusUnknown             StepStatus = "Unknown"
	StepStatusCreated             StepStatus = "Created"
	StepStatusDelete              StepStatus = "Delete"
	StepStatusDeleting            StepStatus = "Deleting"
	StepStatusNotPresent          StepStatus = "NotPresent"
	StepStatusPresent             StepStatus = "Present"
	StepStatusWaitingForAPI       StepStatus = "WaitingForApi"
	StepStatusUnsupportedResource StepStatus = "UnsupportedResource"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// BluePrintSpec defines the desired state of BluePrint
type BluePrintSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	ClusterServiceVersion string `json:"clusterServiceVersionName"`
	OldVersion            string `json:"oldVersion"`
	Generation            int    `json:"generation,omitempty"`

	// Foo is an example field of BluePrint. Edit blueprint_types.go to remove/update
	Foo string `json:"foo,omitempty"`
}

// InstallPlanPhase is the current status of a InstallPlan as a whole.
type BluePrintPhase string

// BluePrintStatus defines the observed state of BluePrint
type BluePrintStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Plan  Step           `json:"plan,omitempty"`
	Phase BluePrintPhase `json:"phase"`
	// BundleLookups is the set of in-progress requests to pull and unpackage bundle content to the cluster.
	// BundleLookups是一个进行中的请求集合（去拉或解开bundle的内容到集群中）
	// +optional
	BundleLookups []BundleLookup `json:"bundleLookups,omitempty"`

	// AttenuatedServiceAccountRef references the service account that is used
	// to do scoped operator install.
	AttenuatedServiceAccountRef *corev1.ObjectReference `json:"attenuatedServiceAccountRef,omitempty"`
}

// +kubebuilder:object:root=true

// BluePrint is the Schema for the BluePrints API
type BluePrint struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BluePrintSpec   `json:"spec,omitempty"`
	Status BluePrintStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BluePrintList contains a list of BluePrint
type BluePrintList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BluePrint `json:"items"`
}

// Step represents the status of an individual step in an InstallPlan.
type Step struct {
	Resolving string       `json:"resolving"`
	Resource  StepResource `json:"resource"`
	Status    StepStatus   `json:"status"`
}

// StepResource represents the status of a resource to be tracked by an
// InstallPlan.
type StepResource struct {
	// CatalogSource          string `json:"sourceName"`
	// CatalogSourceNamespace string `json:"sourceNamespace"`
	Group    string `json:"group,omitempty"`
	Version  string `json:"version,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Name     string `json:"name,omitempty"`
	Manifest string `json:"manifest,omitempty"`
}

// BundleLookup is a request to pull and unpackage the content of a bundle to the cluster.
type BundleLookup struct {
	// Path refers to the location of a bundle to pull.
	// It's typically an image reference.
	Path string `json:"path"`
	// Identifier is the catalog-unique name of the operator (the name of the CSV for bundles that contain CSVs)
	Identifier string `json:"identifier"`
	// Replaces is the name of the bundle to replace with the one found at Path.
	Replaces string `json:"replaces"`
	//// CatalogSourceRef is a reference to the CatalogSource the bundle path was resolved from.
	//CatalogSourceRef *corev1.ObjectReference `json:"catalogSourceRef"`
	// Conditions represents the overall state of a BundleLookup.
	// +optional
	Conditions []BundleLookupCondition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	// The effective properties of the unpacked bundle.
	// +optional
	Properties string `json:"properties,omitempty"`
}

type BundleLookupCondition struct {
	// Type of condition.
	Type BundleLookupConditionType `json:"type"`
	// Status of the condition, one of True, False, Unknown.
	Status corev1.ConditionStatus `json:"status"`
	// The reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`
	// A human readable message indicating details about the transition.
	// +optional
	Message string `json:"message,omitempty"`
	// Last time the condition was probed.
	// +optional
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`
	// Last time the condition transitioned from one status to another.
	// +optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`
}

// BundleLookupConditionType is a category of the overall state of a BundleLookup.
type BundleLookupConditionType string

// StepStatus is the current status of a particular resource an in
// InstallPlan
type StepStatus string

func init() {
	SchemeBuilder.Register(&BluePrint{}, &BluePrintList{})
}
