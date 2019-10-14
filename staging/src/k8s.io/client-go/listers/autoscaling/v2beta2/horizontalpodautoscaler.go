/*
Copyright The Kubernetes Authors.

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

// Code generated by lister-gen. DO NOT EDIT.

package v2beta2

import (
	v2beta2 "k8s.io/api/autoscaling/v2beta2"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// HorizontalPodAutoscalerLister helps list HorizontalPodAutoscalers.
type HorizontalPodAutoscalerLister interface {
	// List lists all HorizontalPodAutoscalers in the indexer.
	List(selector labels.Selector) (ret []*v2beta2.HorizontalPodAutoscaler, err error)
	// HorizontalPodAutoscalers returns an object that can list and get HorizontalPodAutoscalers.
	HorizontalPodAutoscalers(namespace string, optional_tenant ...string) HorizontalPodAutoscalerNamespaceLister
	HorizontalPodAutoscalerListerExpansion
}

// horizontalPodAutoscalerLister implements the HorizontalPodAutoscalerLister interface.
type horizontalPodAutoscalerLister struct {
	indexer cache.Indexer
}

// NewHorizontalPodAutoscalerLister returns a new HorizontalPodAutoscalerLister.
func NewHorizontalPodAutoscalerLister(indexer cache.Indexer) HorizontalPodAutoscalerLister {
	return &horizontalPodAutoscalerLister{indexer: indexer}
}

// List lists all HorizontalPodAutoscalers in the indexer.
func (s *horizontalPodAutoscalerLister) List(selector labels.Selector) (ret []*v2beta2.HorizontalPodAutoscaler, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v2beta2.HorizontalPodAutoscaler))
	})
	return ret, err
}

// HorizontalPodAutoscalers returns an object that can list and get HorizontalPodAutoscalers.
func (s *horizontalPodAutoscalerLister) HorizontalPodAutoscalers(namespace string, optional_tenant ...string) HorizontalPodAutoscalerNamespaceLister {
	tenant := "default"
	if len(optional_tenant) > 0 {
		tenant = optional_tenant[0]
	}
	return horizontalPodAutoscalerNamespaceLister{indexer: s.indexer, namespace: namespace, tenant: tenant}
}

// HorizontalPodAutoscalerNamespaceLister helps list and get HorizontalPodAutoscalers.
type HorizontalPodAutoscalerNamespaceLister interface {
	// List lists all HorizontalPodAutoscalers in the indexer for a given tenant/namespace.
	List(selector labels.Selector) (ret []*v2beta2.HorizontalPodAutoscaler, err error)
	// Get retrieves the HorizontalPodAutoscaler from the indexer for a given tenant/namespace and name.
	Get(name string) (*v2beta2.HorizontalPodAutoscaler, error)
	HorizontalPodAutoscalerNamespaceListerExpansion
}

// horizontalPodAutoscalerNamespaceLister implements the HorizontalPodAutoscalerNamespaceLister
// interface.
type horizontalPodAutoscalerNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
	tenant    string
}

// List lists all HorizontalPodAutoscalers in the indexer for a given namespace.
func (s horizontalPodAutoscalerNamespaceLister) List(selector labels.Selector) (ret []*v2beta2.HorizontalPodAutoscaler, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.tenant, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v2beta2.HorizontalPodAutoscaler))
	})
	return ret, err
}

// Get retrieves the HorizontalPodAutoscaler from the indexer for a given namespace and name.
func (s horizontalPodAutoscalerNamespaceLister) Get(name string) (*v2beta2.HorizontalPodAutoscaler, error) {
	key := s.tenant + "/" + s.namespace + "/" + name
	if s.tenant == "default" {
		key = s.namespace + "/" + name
	}
	obj, exists, err := s.indexer.GetByKey(key)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v2beta2.Resource("horizontalpodautoscaler"), name)
	}
	return obj.(*v2beta2.HorizontalPodAutoscaler), nil
}
