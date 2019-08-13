package k8s

import (
	"edp-admin-console/models"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"
)

// +k8s:openapi-gen=true
type StageSpec struct {
	Name         string               `json:"name"`
	CdPipeline   string               `json:"cdPipeline"`
	Description  string               `json:"description"`
	TriggerType  string               `json:"triggerType"`
	Order        int                  `json:"order"`
	QualityGates []models.QualityGate `json:"qualityGates"`
}

// +k8s:openapi-gen=true
type StageStatus struct {
	Available       bool      `json:"available"`
	LastTimeUpdated time.Time `json:"last_time_updated"`
	Status          string    `json:"status"`
	Username        string    `json:"username"`
	Action          string    `json:"action"`
	Result          string    `json:"result"`
	Value           string    `json:"value"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// +k8s:openapi-gen=true
type Stage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StageSpec   `json:"spec,omitempty"`
	Status StageStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// StageList contains a list of Stage
type StageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Stage `json:"items"`
}
