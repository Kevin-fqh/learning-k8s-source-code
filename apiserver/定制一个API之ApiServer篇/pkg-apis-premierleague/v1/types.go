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

package v1

import (
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/api/v1"
)

// +genclient=true

// Match contains two teams
// Status will add in the future
type Match struct {
	unversioned.TypeMeta `json:",inline"`
	// opt 表示是可选类型(还有require类型)
	// +optional
	v1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	// Specification of the desired two teams.
	// +optional
	Spec MatchSpec `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
}

type MatchList struct {
	unversioned.TypeMeta `json:",inline"`
	// +optional
	unversioned.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	// req 表示数组、切片
	//Items is the list of Match
	Items []Match `json:"items" protobuf:"bytes,2,rep,name=items"`
}

type MatchSpec struct {
	// +optional
	Host string `json:"host,omitempty" protobuf:"bytes,1,opt,name=host"`
	// +optional
	Guest string `json:"guest,omitempty" protobuf:"bytes,2,opt,name=guest"`
}
