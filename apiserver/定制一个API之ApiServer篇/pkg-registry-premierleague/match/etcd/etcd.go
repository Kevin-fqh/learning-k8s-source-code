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
package etcd

import (
	"k8s.io/kubernetes/pkg/api"
	premierleague "k8s.io/kubernetes/pkg/apis/premierleague"
	"k8s.io/kubernetes/pkg/registry/cachesize"
	"k8s.io/kubernetes/pkg/registry/generic"
	"k8s.io/kubernetes/pkg/registry/generic/registry"
	"k8s.io/kubernetes/pkg/registry/premierleague/match"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/storage"
)

// MatchStorage includes storage for matchs and all sub resources
type MatchStorage struct {
	Match *REST
	// add status in the future
}

type REST struct {
	*registry.Store
}

func NewStorage(opts generic.RESTOptions) MatchStorage {
	matchREST := NewREST(opts)

	return MatchStorage{
		Match: matchREST,
	}
}

// NewREST returns a RESTStorage object that will work against matchs.
func NewREST(opts generic.RESTOptions) *REST {
	prefix := "/" + opts.ResourcePrefix
	newListFunc := func() runtime.Object {
		return &premierleague.MatchList{}
	}

	storageInterface, dFunc := opts.Decorator(
		opts.StorageConfig,
		// implementate it
		cachesize.GetWatchCacheSizeByResource(cachesize.Matchs),
		&premierleague.Match{},
		prefix,
		// implementate it
		match.Strategy,
		newListFunc,
		storage.NoTriggerPublisher,
	)

	store := &registry.Store{
		NewFunc:     func() runtime.Object { return &premierleague.Match{} },
		NewListFunc: newListFunc,
		KeyRootFunc: func(ctx api.Context) string {
			return registry.NamespaceKeyRootFunc(ctx, prefix)
		},
		KeyFunc: func(ctx api.Context, id string) (string, error) {
			return registry.NamespaceKeyFunc(ctx, prefix, id)
		},
		ObjectNameFunc: func(obj runtime.Object) (string, error) {
			return obj.(*premierleague.Match).Name, nil
		},
		//注意这里的MatchMatch，前面是固定格式，后面是资源Match
		PredicateFunc:           match.MatchMatch,
		QualifiedResource:       premierleague.Resource("matchs"),
		EnableGarbageCollection: opts.EnableGarbageCollection,
		DeleteCollectionWorkers: opts.DeleteCollectionWorkers,

		CreateStrategy: match.Strategy,
		UpdateStrategy: match.Strategy,
		DeleteStrategy: match.Strategy,

		Storage:     storageInterface,
		DestroyFunc: dFunc,
	}

	return &REST{store}
}
