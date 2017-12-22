/*
Copyright 2016 The Kubernetes Authors.

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

package premierleague

import (
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/unversioned"
)

// +genclient=true

// Match contains two teams
// Status will add in the future
type Match struct {
	unversioned.TypeMeta `json:",inline"`
	// +optional
	api.ObjectMeta `json:"metadata,omitempty"`

	// Specification of the desired two teams.
	// +optional
	Spec MatchSpec `json:"spec,omitempty"`
}

type MatchList struct {
	unversioned.TypeMeta `json:",inline"`
	// +optional
	unversioned.ListMeta `json:"metadata,omitempty"`

	//Items is the list of Match
	Items []Match `json:"items"`
}

type MatchSpec struct {
	// +optional
	Host string `json:"host,omitempty"`
	// +optional
	Guest string `json:"guest,omitempty"`
	//	matchSelector unversioned.LabelSelector `json:"matchSelector"`
}
