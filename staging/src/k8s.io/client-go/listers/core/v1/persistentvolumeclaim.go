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

package v1

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// PersistentVolumeClaimLister helps list PersistentVolumeClaims.
type PersistentVolumeClaimLister interface {
	// List lists all PersistentVolumeClaims in the indexer.
	List(selector labels.Selector) (ret []*v1.PersistentVolumeClaim, err error)
	// PersistentVolumeClaims returns an object that can list and get PersistentVolumeClaims.
	PersistentVolumeClaims(namespace string, optional_tenant ...string) PersistentVolumeClaimNamespaceLister
	PersistentVolumeClaimListerExpansion
}

// persistentVolumeClaimLister implements the PersistentVolumeClaimLister interface.
type persistentVolumeClaimLister struct {
	indexer cache.Indexer
}

// NewPersistentVolumeClaimLister returns a new PersistentVolumeClaimLister.
func NewPersistentVolumeClaimLister(indexer cache.Indexer) PersistentVolumeClaimLister {
	return &persistentVolumeClaimLister{indexer: indexer}
}

// List lists all PersistentVolumeClaims in the indexer.
func (s *persistentVolumeClaimLister) List(selector labels.Selector) (ret []*v1.PersistentVolumeClaim, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1.PersistentVolumeClaim))
	})
	return ret, err
}

// PersistentVolumeClaims returns an object that can list and get PersistentVolumeClaims.
func (s *persistentVolumeClaimLister) PersistentVolumeClaims(namespace string, optional_tenant ...string) PersistentVolumeClaimNamespaceLister {
	tenant := "default"
	if len(optional_tenant) > 0 {
		tenant = optional_tenant[0]
	}
	return persistentVolumeClaimNamespaceLister{indexer: s.indexer, namespace: namespace, tenant: tenant}
}

// PersistentVolumeClaimNamespaceLister helps list and get PersistentVolumeClaims.
type PersistentVolumeClaimNamespaceLister interface {
	// List lists all PersistentVolumeClaims in the indexer for a given tenant/namespace.
	List(selector labels.Selector) (ret []*v1.PersistentVolumeClaim, err error)
	// Get retrieves the PersistentVolumeClaim from the indexer for a given tenant/namespace and name.
	Get(name string) (*v1.PersistentVolumeClaim, error)
	PersistentVolumeClaimNamespaceListerExpansion
}

// persistentVolumeClaimNamespaceLister implements the PersistentVolumeClaimNamespaceLister
// interface.
type persistentVolumeClaimNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
	tenant    string
}

// List lists all PersistentVolumeClaims in the indexer for a given namespace.
func (s persistentVolumeClaimNamespaceLister) List(selector labels.Selector) (ret []*v1.PersistentVolumeClaim, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.tenant, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1.PersistentVolumeClaim))
	})
	return ret, err
}

// Get retrieves the PersistentVolumeClaim from the indexer for a given namespace and name.
func (s persistentVolumeClaimNamespaceLister) Get(name string) (*v1.PersistentVolumeClaim, error) {
	key := s.tenant + "/" + s.namespace + "/" + name
	if s.tenant == "default" {
		key = s.namespace + "/" + name
	}
	obj, exists, err := s.indexer.GetByKey(key)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1.Resource("persistentvolumeclaim"), name)
	}
	return obj.(*v1.PersistentVolumeClaim), nil
}
