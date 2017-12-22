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

package validation

import (
	apivalidation "k8s.io/kubernetes/pkg/api/validation"
	"k8s.io/kubernetes/pkg/apis/premierleague"
	"k8s.io/kubernetes/pkg/util/validation/field"
)

var ValidateMatchName = apivalidation.NameIsDNSSubdomain

func ValidateMatch(obj *premierleague.Match) field.ErrorList {
	allErrs := apivalidation.ValidateObjectMeta(&obj.ObjectMeta, true, ValidateMatchName, field.NewPath("metadata"))
	allErrs = append(allErrs, ValidateMatchSpec(&obj.Spec, field.NewPath("spec"))...)
	return allErrs
}

func ValidateMatchUpdate(update, old *premierleague.Match) field.ErrorList {
	allErrs := apivalidation.ValidateObjectMetaUpdate(&update.ObjectMeta, &old.ObjectMeta, field.NewPath("metadata"))
	allErrs = append(allErrs, ValidateMatchSpec(&update.Spec, field.NewPath("spec"))...)
	return allErrs
}

// Validates given match spec.
func ValidateMatchSpec(spec *premierleague.MatchSpec, fldPath *field.Path) field.ErrorList {
	//根据前面Type的定义来判断Spec的有效性
	allErrs := field.ErrorList{}
	if spec.Host == "" {
		allErrs = append(allErrs, field.Required(fldPath.Child("Host"), "at least 1 Host is required"))
	}
	if spec.Guest == "" {
		allErrs = append(allErrs, field.Required(fldPath.Child("Guest"), "at least 1 Guest is required"))
	}
	return allErrs
}
