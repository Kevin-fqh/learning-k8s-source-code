/*
Copyright 2015 The Kubernetes Authors.

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

package match

import (
	"fmt"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/apis/premierleague"
	"k8s.io/kubernetes/pkg/apis/premierleague/validation" //待实现
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/labels"
	"k8s.io/kubernetes/pkg/registry/generic"
	"k8s.io/kubernetes/pkg/runtime"
	apistorage "k8s.io/kubernetes/pkg/storage"
	"k8s.io/kubernetes/pkg/util/validation/field"
)

// matchStrategy implements behavior for Matchs.
type matchStrategy struct {
	runtime.ObjectTyper
	api.NameGenerator
}

// Strategy is the default logic that applies when creating and updating Match
// objects via the REST API.
var Strategy = matchStrategy{api.Scheme, api.SimpleNameGenerator}

// NamespaceScoped is true for match.
func (matchStrategy) NamespaceScoped() bool {
	return true
}

func (matchStrategy) PrepareForCreate(ctx api.Context, obj runtime.Object) {
	match := obj.(*premierleague.Match)
	match.Generation = 1
}

// Validate validates a new match.
func (matchStrategy) Validate(ctx api.Context, obj runtime.Object) field.ErrorList {
	match := obj.(*premierleague.Match)
	return validation.ValidateMatch(match)
}

// Canonicalize normalizes the object after validation.
func (matchStrategy) Canonicalize(obj runtime.Object) {
}

// AllowCreateOnUpdate is false for match.
func (matchStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (matchStrategy) PrepareForUpdate(ctx api.Context, obj, old runtime.Object) {
}

// ValidateUpdate is the default update validation for an end user.
func (matchStrategy) ValidateUpdate(ctx api.Context, obj, old runtime.Object) field.ErrorList {
	return validation.ValidateMatchUpdate(obj.(*premierleague.Match), old.(*premierleague.Match))
}

func (matchStrategy) AllowUnconditionalUpdate() bool {
	return true
}

// MatchToSelectableFields returns a field set that represents the object.
func MatchToSelectableFields(match *premierleague.Match) fields.Set {
	return generic.ObjectMetaFieldsSet(&match.ObjectMeta, true)
}

// MatchMatch is the filter used by the generic etcd backend to route
// watch events from etcd to clients of the apiserver only interested in specific
// labels/fields.
func MatchMatch(label labels.Selector, field fields.Selector) apistorage.SelectionPredicate {
	return apistorage.SelectionPredicate{
		Label: label,
		Field: field,
		GetAttrs: func(obj runtime.Object) (labels.Set, fields.Set, error) {
			match, ok := obj.(*premierleague.Match)
			if !ok {
				return nil, nil, fmt.Errorf("given object is not a match.")
			}
			return labels.Set(match.ObjectMeta.Labels), MatchToSelectableFields(match), nil
		},
	}
}
