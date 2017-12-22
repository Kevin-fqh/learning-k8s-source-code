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

package rest

import (
	"k8s.io/kubernetes/pkg/api/rest"
	"k8s.io/kubernetes/pkg/apis/premierleague"
	premierleagueapiv1 "k8s.io/kubernetes/pkg/apis/premierleague/v1"
	"k8s.io/kubernetes/pkg/genericapiserver"
	matchetcd "k8s.io/kubernetes/pkg/registry/premierleague/match/etcd"
)

type RESTStorageProvider struct{}

var _ genericapiserver.RESTStorageProvider = &RESTStorageProvider{}

func (p RESTStorageProvider) NewRESTStorage(apiResourceConfigSource genericapiserver.APIResourceConfigSource, restOptionsGetter genericapiserver.RESTOptionsGetter) (genericapiserver.APIGroupInfo, bool) {
	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(premierleague.GroupName)

	if apiResourceConfigSource.AnyResourcesForVersionEnabled(premierleagueapiv1.SchemeGroupVersion) {
		//k8s中所有的资源都会注册到 VersionedResourcesStorageMap 中，后面设计路由path时会用到VersionedResourcesStorageMap
		apiGroupInfo.VersionedResourcesStorageMap[premierleagueapiv1.SchemeGroupVersion.Version] = p.v1Storage(apiResourceConfigSource, restOptionsGetter)
		apiGroupInfo.GroupMeta.GroupVersion = premierleagueapiv1.SchemeGroupVersion
	}

	return apiGroupInfo, true
}

func (p RESTStorageProvider) v1Storage(apiResourceConfigSource genericapiserver.APIResourceConfigSource, restOptionsGetter genericapiserver.RESTOptionsGetter) map[string]rest.Storage {
	version := premierleagueapiv1.SchemeGroupVersion

	storage := map[string]rest.Storage{}
	//注册资源matchs
	if apiResourceConfigSource.ResourceEnabled(version.WithResource("matchs")) {
		matchStorage := matchetcd.NewStorage(restOptionsGetter(premierleague.Resource("matchs")))
		storage["matchs"] = matchStorage.Match
	}
	return storage
}

func (p RESTStorageProvider) GroupName() string {
	return premierleague.GroupName
}
