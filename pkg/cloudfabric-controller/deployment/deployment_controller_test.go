/*
Copyright 2020 Authors of Alkaid.

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

package deployment

import (
	"k8s.io/kubernetes/pkg/cloudfabric-controller/controllerframework"
	"strconv"
	"testing"

	apps "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
	_ "k8s.io/kubernetes/pkg/apis/apps/install"
	_ "k8s.io/kubernetes/pkg/apis/authentication/install"
	_ "k8s.io/kubernetes/pkg/apis/authorization/install"
	_ "k8s.io/kubernetes/pkg/apis/autoscaling/install"
	_ "k8s.io/kubernetes/pkg/apis/batch/install"
	_ "k8s.io/kubernetes/pkg/apis/certificates/install"
	_ "k8s.io/kubernetes/pkg/apis/core/install"
	_ "k8s.io/kubernetes/pkg/apis/policy/install"
	_ "k8s.io/kubernetes/pkg/apis/rbac/install"
	_ "k8s.io/kubernetes/pkg/apis/settings/install"
	_ "k8s.io/kubernetes/pkg/apis/storage/install"
	"k8s.io/kubernetes/pkg/cloudfabric-controller"
	"k8s.io/kubernetes/pkg/cloudfabric-controller/deployment/util"
	"k8s.io/kubernetes/pkg/cloudfabric-controller/testutil"
)

var (
	alwaysReady = func() bool { return true }
	noTimestamp = metav1.Time{}
)

func rs(name string, replicas int, selector map[string]string, timestamp metav1.Time, tenant string) *apps.ReplicaSet {
	return &apps.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			CreationTimestamp: timestamp,
			Namespace:         metav1.NamespaceDefault,
			Tenant:            tenant,
		},
		Spec: apps.ReplicaSetSpec{
			Replicas: func() *int32 { i := int32(replicas); return &i }(),
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: v1.PodTemplateSpec{},
		},
	}
}

func newRSWithStatus(name string, specReplicas, statusReplicas int, selector map[string]string, tenant string) *apps.ReplicaSet {
	rs := rs(name, specReplicas, selector, noTimestamp, tenant)
	rs.Status = apps.ReplicaSetStatus{
		Replicas: int32(statusReplicas),
	}
	return rs
}

func newControllerInstance(controllerType, tenant string) *v1.ControllerInstance {
	ci := v1.ControllerInstance{
		ControllerType: controllerType,
	}

	return &ci
}

func newDeployment(name string, replicas int, revisionHistoryLimit *int32, maxSurge, maxUnavailable *intstr.IntOrString, selector map[string]string, tenant string) *apps.Deployment {
	d := apps.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			UID:         uuid.NewUUID(),
			Name:        name,
			Namespace:   metav1.NamespaceDefault,
			Tenant:      tenant,
			Annotations: make(map[string]string),
		},
		Spec: apps.DeploymentSpec{
			Strategy: apps.DeploymentStrategy{
				Type: apps.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &apps.RollingUpdateDeployment{
					MaxUnavailable: func() *intstr.IntOrString { i := intstr.FromInt(0); return &i }(),
					MaxSurge:       func() *intstr.IntOrString { i := intstr.FromInt(0); return &i }(),
				},
			},
			Replicas: func() *int32 { i := int32(replicas); return &i }(),
			Selector: &metav1.LabelSelector{MatchLabels: selector},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selector,
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Image: "foo/bar",
						},
					},
				},
			},
			RevisionHistoryLimit: revisionHistoryLimit,
		},
	}
	if maxSurge != nil {
		d.Spec.Strategy.RollingUpdate.MaxSurge = maxSurge
	}
	if maxUnavailable != nil {
		d.Spec.Strategy.RollingUpdate.MaxUnavailable = maxUnavailable
	}
	return &d
}

func newReplicaSet(d *apps.Deployment, name string, replicas int, tenant string) *apps.ReplicaSet {
	return &apps.ReplicaSet{
		TypeMeta: metav1.TypeMeta{Kind: "ReplicaSet"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			UID:             uuid.NewUUID(),
			Namespace:       metav1.NamespaceDefault,
			Tenant:          tenant,
			Labels:          d.Spec.Selector.MatchLabels,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(d, controllerKind)},
		},
		Spec: apps.ReplicaSetSpec{
			Selector: d.Spec.Selector,
			Replicas: func() *int32 { i := int32(replicas); return &i }(),
			Template: d.Spec.Template,
		},
	}
}

type fixture struct {
	t *testing.T

	client *fake.Clientset
	// Objects to put in the store.
	dLister   []*apps.Deployment
	rsLister  []*apps.ReplicaSet
	podLister []*v1.Pod

	// Actions expected to happen on the client. Objects from here are also
	// preloaded into NewSimpleFake.
	actions []core.Action
	objects []runtime.Object
}

func (f *fixture) expectGetDeploymentAction(d *apps.Deployment) {
	action := core.NewGetActionWithMultiTenancy(schema.GroupVersionResource{Resource: "deployments"}, d.Namespace, d.Name, d.Tenant)
	f.actions = append(f.actions, action)
}

func (f *fixture) expectUpdateDeploymentStatusAction(d *apps.Deployment) {
	action := core.NewUpdateActionWithMultiTenancy(schema.GroupVersionResource{Resource: "deployments"}, d.Namespace, d, d.Tenant)
	action.Subresource = "status"
	f.actions = append(f.actions, action)
}

func (f *fixture) expectUpdateDeploymentAction(d *apps.Deployment) {
	action := core.NewUpdateActionWithMultiTenancy(schema.GroupVersionResource{Resource: "deployments"}, d.Namespace, d, d.Tenant)
	f.actions = append(f.actions, action)
}

func (f *fixture) expectCreateRSAction(rs *apps.ReplicaSet) {
	f.actions = append(f.actions, core.NewCreateActionWithMultiTenancy(schema.GroupVersionResource{Resource: "replicasets"}, rs.Namespace, rs, rs.Tenant))
}

func (f *fixture) expectCreateControllerInstanceAction(ci *v1.ControllerInstance) {
	f.actions = append(f.actions, core.NewCreateActionWithMultiTenancy(schema.GroupVersionResource{Resource: "controllerinstances"}, ci.Namespace, ci, ci.Tenant))
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{}
	f.t = t
	f.objects = []runtime.Object{}
	return f
}

func (f *fixture) newController() (*DeploymentController, informers.SharedInformerFactory, error) {
	f.client = fake.NewSimpleClientset(f.objects...)
	informers := informers.NewSharedInformerFactory(f.client, controller.NoResyncPeriodFunc())

	stopCh := make(chan struct{})
	defer close(stopCh)
	cimUpdateCh, informersResetChGrp := controllerframework.MockCreateControllerInstanceAndResetChs(stopCh)

	c, err := NewDeploymentController(informers.Apps().V1().Deployments(), informers.Apps().V1().ReplicaSets(), informers.Core().V1().Pods(), f.client, cimUpdateCh, informersResetChGrp)
	if err != nil {
		return nil, nil, err
	}
	c.eventRecorder = &record.FakeRecorder{}
	c.dListerSynced = alwaysReady
	c.rsListerSynced = alwaysReady
	c.podListerSynced = alwaysReady
	for _, d := range f.dLister {
		informers.Apps().V1().Deployments().Informer().GetIndexer().Add(d)
	}
	for _, rs := range f.rsLister {
		informers.Apps().V1().ReplicaSets().Informer().GetIndexer().Add(rs)
	}
	for _, pod := range f.podLister {
		informers.Core().V1().Pods().Informer().GetIndexer().Add(pod)
	}
	return c, informers, nil
}

func (f *fixture) runExpectError(deploymentName string, startInformers bool) {
	f.run_(deploymentName, startInformers, true)
}

func (f *fixture) run(deploymentName string) {
	f.run_(deploymentName, true, false)
}

func (f *fixture) run_(deploymentName string, startInformers bool, expectError bool) {
	c, informers, err := f.newController()
	if err != nil {
		f.t.Fatalf("error creating Deployment controller: %v", err)
	}
	if startInformers {
		stopCh := make(chan struct{})
		defer close(stopCh)
		informers.Start(stopCh)
	}

	err = c.syncDeployment(deploymentName)
	if !expectError && err != nil {
		f.t.Errorf("error syncing deployment: %v", err)
	} else if expectError && err == nil {
		f.t.Error("expected error syncing deployment, got nil")
	}

	actions := filterInformerActions(f.client.Actions())
	for i, action := range actions {
		if len(f.actions) < i+1 {
			f.t.Errorf("%d unexpected actions: %+v", len(actions)-len(f.actions), actions[i:])
			break
		}

		expectedAction := f.actions[i]
		if !(expectedAction.Matches(action.GetVerb(), action.GetResource().Resource) && action.GetSubresource() == expectedAction.GetSubresource()) {
			f.t.Errorf("Expected\n\t%#v\ngot\n\t%#v", expectedAction, action)
			continue
		}
	}

	if len(f.actions) > len(actions) {
		f.t.Errorf("%d additional expected actions:%+v", len(f.actions)-len(actions), f.actions[len(actions):])
	}
}

func filterInformerActions(actions []core.Action) []core.Action {
	ret := []core.Action{}
	for _, action := range actions {
		if len(action.GetNamespace()) == 0 && len(action.GetTenant()) == 0 &&
			(action.Matches("list", "pods") ||
				action.Matches("list", "deployments") ||
				action.Matches("list", "replicasets") ||
				action.Matches("watch", "pods") ||
				action.Matches("watch", "deployments") ||
				action.Matches("watch", "replicasets")) {
			continue
		}
		ret = append(ret, action)
	}

	return ret
}

func TestSyncDeploymentCreatesReplicaSet(t *testing.T) {
	testSyncDeploymentCreatesReplicaSet(t, metav1.TenantDefault)
}

func TestSyncDeploymentCreatesReplicaSetWithMultiTenancy(t *testing.T) {
	testSyncDeploymentCreatesReplicaSet(t, "test-te")
}

func testSyncDeploymentCreatesReplicaSet(t *testing.T, tenant string) {
	f := newFixture(t)

	d := newDeployment("foo", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	f.dLister = append(f.dLister, d)
	f.objects = append(f.objects, d)

	rs := newReplicaSet(d, "deploymentrs-4186632231", 1, tenant)

	ci := newControllerInstance("Deployment", tenant)
	f.expectCreateControllerInstanceAction(ci)

	f.expectCreateRSAction(rs)
	f.expectUpdateDeploymentStatusAction(d)
	f.expectUpdateDeploymentStatusAction(d)

	f.run(testutil.GetKey(d, t))
}

func TestSyncDeploymentDontDoAnythingDuringDeletion(t *testing.T) {
	testSyncDeploymentDontDoAnythingDuringDeletion(t, metav1.TenantDefault)
}

func TestSyncDeploymentDontDoAnythingDuringDeletionWithMultiTenancy(t *testing.T) {
	testSyncDeploymentDontDoAnythingDuringDeletion(t, "test-te")
}

func testSyncDeploymentDontDoAnythingDuringDeletion(t *testing.T, tenant string) {
	f := newFixture(t)

	d := newDeployment("foo", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	now := metav1.Now()
	d.DeletionTimestamp = &now
	f.dLister = append(f.dLister, d)
	f.objects = append(f.objects, d)

	ci := newControllerInstance("Deployment", tenant)
	f.expectCreateControllerInstanceAction(ci)

	f.expectUpdateDeploymentStatusAction(d)
	f.run(testutil.GetKey(d, t))
}

func TestSyncDeploymentDeletionRace(t *testing.T) {
	testSyncDeploymentDeletionRace(t, metav1.TenantDefault)
}

func TestSyncDeploymentDeletionRaceWithMultiTenancy(t *testing.T) {
	testSyncDeploymentDeletionRace(t, "test-te")
}

func testSyncDeploymentDeletionRace(t *testing.T, tenant string) {
	f := newFixture(t)

	d := newDeployment("foo", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d2 := *d
	// Lister (cache) says NOT deleted.
	f.dLister = append(f.dLister, d)
	// Bare client says it IS deleted. This should be presumed more up-to-date.
	now := metav1.Now()
	d2.DeletionTimestamp = &now
	f.objects = append(f.objects, &d2)

	// The recheck is only triggered if a matching orphan exists.
	rs := newReplicaSet(d, "rs1", 1, tenant)
	rs.OwnerReferences = nil
	f.objects = append(f.objects, rs)
	f.rsLister = append(f.rsLister, rs)

	ci := newControllerInstance("Deployment", tenant)
	f.expectCreateControllerInstanceAction(ci)

	// Expect to only recheck DeletionTimestamp.
	f.expectGetDeploymentAction(d)
	// Sync should fail and requeue to let cache catch up.
	// Don't start informers, since we don't want cache to catch up for this test.
	f.runExpectError(testutil.GetKey(d, t), false)
}

func TestDontSyncDeploymentsWithEmptyPodSelector(t *testing.T) {
	testDontSyncDeploymentsWithEmptyPodSelector(t, metav1.TenantDefault)
}

func TestDontSyncDeploymentsWithEmptyPodSelectorWithMultiTenancy(t *testing.T) {
	testDontSyncDeploymentsWithEmptyPodSelector(t, "test-te")
}

// issue: https://github.com/kubernetes/kubernetes/issues/23218
func testDontSyncDeploymentsWithEmptyPodSelector(t *testing.T, tenant string) {
	f := newFixture(t)

	d := newDeployment("foo", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d.Spec.Selector = &metav1.LabelSelector{}
	f.dLister = append(f.dLister, d)
	f.objects = append(f.objects, d)

	ci := newControllerInstance("Deployment", tenant)
	f.expectCreateControllerInstanceAction(ci)

	// Normally there should be a status update to sync observedGeneration but the fake
	// deployment has no generation set so there is no action happpening here.
	f.run(testutil.GetKey(d, t))
}

func TestReentrantRollback(t *testing.T) {
	testReentrantRollback(t, metav1.TenantDefault)
}

func TestReentrantRollbackWithMultiTenancy(t *testing.T) {
	testReentrantRollback(t, "test-te")
}

func testReentrantRollback(t *testing.T, tenant string) {
	f := newFixture(t)

	d := newDeployment("foo", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d.Annotations = map[string]string{util.RevisionAnnotation: "2"}
	setRollbackTo(d, &extensions.RollbackConfig{Revision: 0})
	f.dLister = append(f.dLister, d)

	rs1 := newReplicaSet(d, "deploymentrs-old", 0, tenant)
	rs1.Annotations = map[string]string{util.RevisionAnnotation: "1"}
	one := int64(1)
	rs1.Spec.Template.Spec.TerminationGracePeriodSeconds = &one
	rs1.Spec.Selector.MatchLabels[apps.DefaultDeploymentUniqueLabelKey] = "hash"

	rs2 := newReplicaSet(d, "deploymentrs-new", 1, tenant)
	rs2.Annotations = map[string]string{util.RevisionAnnotation: "2"}
	rs2.Spec.Selector.MatchLabels[apps.DefaultDeploymentUniqueLabelKey] = "hash"

	f.rsLister = append(f.rsLister, rs1, rs2)
	f.objects = append(f.objects, d, rs1, rs2)

	ci := newControllerInstance("Deployment", tenant)
	f.expectCreateControllerInstanceAction(ci)

	// Rollback is done here
	f.expectUpdateDeploymentAction(d)
	// Expect no update on replica sets though
	f.run(testutil.GetKey(d, t))
}

// TestPodDeletionEnqueuesRecreateDeployment ensures that the deletion of a pod
// will requeue a Recreate deployment iff there is no other pod returned from the
// client.
func TestPodDeletionEnqueuesRecreateDeployment(t *testing.T) {
	testPodDeletionEnqueuesRecreateDeployment(t, metav1.TenantDefault)
}

func TestPodDeletionEnqueuesRecreateDeploymentWithMultiTenancy(t *testing.T) {
	testPodDeletionEnqueuesRecreateDeployment(t, "test-te")
}

func testPodDeletionEnqueuesRecreateDeployment(t *testing.T, tenant string) {
	f := newFixture(t)

	foo := newDeployment("foo", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	foo.Spec.Strategy.Type = apps.RecreateDeploymentStrategyType
	rs := newReplicaSet(foo, "foo-1", 1, tenant)
	pod := generatePodFromRS(rs)

	f.dLister = append(f.dLister, foo)
	f.rsLister = append(f.rsLister, rs)
	f.objects = append(f.objects, foo, rs)

	c, _, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}
	enqueued := false
	c.enqueueDeployment = func(d *apps.Deployment) {
		if d.Name == "foo" {
			enqueued = true
		}
	}

	c.deletePod(pod)

	if !enqueued {
		t.Errorf("expected deployment %q to be queued after pod deletion", foo.Name)
	}
}

// TestPodDeletionDoesntEnqueueRecreateDeployment ensures that the deletion of a pod
// will not requeue a Recreate deployment iff there are other pods returned from the
// client.
func TestPodDeletionDoesntEnqueueRecreateDeployment(t *testing.T) {
	testPodDeletionDoesntEnqueueRecreateDeployment(t, metav1.TenantDefault)
}

func TestPodDeletionDoesntEnqueueRecreateDeploymentWithMultiTenancy(t *testing.T) {
	testPodDeletionDoesntEnqueueRecreateDeployment(t, "test-te")
}

func testPodDeletionDoesntEnqueueRecreateDeployment(t *testing.T, tenant string) {
	f := newFixture(t)

	foo := newDeployment("foo", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	foo.Spec.Strategy.Type = apps.RecreateDeploymentStrategyType
	rs1 := newReplicaSet(foo, "foo-1", 1, tenant)
	rs2 := newReplicaSet(foo, "foo-1", 1, tenant)
	pod1 := generatePodFromRS(rs1)
	pod2 := generatePodFromRS(rs2)

	f.dLister = append(f.dLister, foo)
	// Let's pretend this is a different pod. The gist is that the pod lister needs to
	// return a non-empty list.
	f.podLister = append(f.podLister, pod1, pod2)

	c, _, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}
	enqueued := false
	c.enqueueDeployment = func(d *apps.Deployment) {
		if d.Name == "foo" {
			enqueued = true
		}
	}

	c.deletePod(pod1)

	if enqueued {
		t.Errorf("expected deployment %q not to be queued after pod deletion", foo.Name)
	}
}

// TestPodDeletionPartialReplicaSetOwnershipEnqueueRecreateDeployment ensures that
// the deletion of a pod will requeue a Recreate deployment iff there is no other
// pod returned from the client in the case where a deployment has multiple replica
// sets, some of which have empty owner references.
func TestPodDeletionPartialReplicaSetOwnershipEnqueueRecreateDeployment(t *testing.T) {
	testPodDeletionPartialReplicaSetOwnershipEnqueueRecreateDeployment(t, metav1.TenantDefault)
}

func TestPodDeletionPartialReplicaSetOwnershipEnqueueRecreateDeploymentWithMultiTenancy(t *testing.T) {
	testPodDeletionPartialReplicaSetOwnershipEnqueueRecreateDeployment(t, "test-te")
}

func testPodDeletionPartialReplicaSetOwnershipEnqueueRecreateDeployment(t *testing.T, tenant string) {
	f := newFixture(t)

	foo := newDeployment("foo", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	foo.Spec.Strategy.Type = apps.RecreateDeploymentStrategyType
	rs1 := newReplicaSet(foo, "foo-1", 1, tenant)
	rs2 := newReplicaSet(foo, "foo-2", 1, tenant)
	rs2.OwnerReferences = nil
	pod := generatePodFromRS(rs1)

	f.dLister = append(f.dLister, foo)
	f.rsLister = append(f.rsLister, rs1, rs2)
	f.objects = append(f.objects, foo, rs1, rs2)

	c, _, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}
	enqueued := false
	c.enqueueDeployment = func(d *apps.Deployment) {
		if d.Name == "foo" {
			enqueued = true
		}
	}

	c.deletePod(pod)

	if !enqueued {
		t.Errorf("expected deployment %q to be queued after pod deletion", foo.Name)
	}
}

// TestPodDeletionPartialReplicaSetOwnershipDoesntEnqueueRecreateDeployment that the
// deletion of a pod will not requeue a Recreate deployment iff there are other pods
// returned from the client in the case where a deployment has multiple replica sets,
// some of which have empty owner references.

func TestPodDeletionPartialReplicaSetOwnershipDoesntEnqueueRecreateDeployment(t *testing.T) {
	testPodDeletionPartialReplicaSetOwnershipDoesntEnqueueRecreateDeployment(t, metav1.TenantDefault)
}

func TestPodDeletionPartialReplicaSetOwnershipDoesntEnqueueRecreateDeploymentWithMultiTenancy(t *testing.T) {
	testPodDeletionPartialReplicaSetOwnershipDoesntEnqueueRecreateDeployment(t, "test-te")

}
func testPodDeletionPartialReplicaSetOwnershipDoesntEnqueueRecreateDeployment(t *testing.T, tenant string) {
	f := newFixture(t)

	foo := newDeployment("foo", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	foo.Spec.Strategy.Type = apps.RecreateDeploymentStrategyType
	rs1 := newReplicaSet(foo, "foo-1", 1, tenant)
	rs2 := newReplicaSet(foo, "foo-2", 1, tenant)
	rs2.OwnerReferences = nil
	pod := generatePodFromRS(rs1)

	f.dLister = append(f.dLister, foo)
	f.rsLister = append(f.rsLister, rs1, rs2)
	f.objects = append(f.objects, foo, rs1, rs2)
	// Let's pretend this is a different pod. The gist is that the pod lister needs to
	// return a non-empty list.
	f.podLister = append(f.podLister, pod)

	c, _, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}
	enqueued := false
	c.enqueueDeployment = func(d *apps.Deployment) {
		if d.Name == "foo" {
			enqueued = true
		}
	}

	c.deletePod(pod)

	if enqueued {
		t.Errorf("expected deployment %q not to be queued after pod deletion", foo.Name)
	}
}

func TestGetReplicaSetsForDeployment(t *testing.T) {

	testGetReplicaSetsForDeployment(t, metav1.TenantDefault)
}

func TestGetReplicaSetsForDeploymentWithMultiTenancy(t *testing.T) {
	testGetReplicaSetsForDeployment(t, "test-te")
}

func testGetReplicaSetsForDeployment(t *testing.T, tenant string) {
	f := newFixture(t)

	// Two Deployments with same labels.
	d1 := newDeployment("foo", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d2 := newDeployment("bar", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)

	// Two ReplicaSets that match labels for both Deployments,
	// but have ControllerRefs to make ownership explicit.
	rs1 := newReplicaSet(d1, "rs1", 1, tenant)
	rs2 := newReplicaSet(d2, "rs2", 1, tenant)

	f.dLister = append(f.dLister, d1, d2)
	f.rsLister = append(f.rsLister, rs1, rs2)
	f.objects = append(f.objects, d1, d2, rs1, rs2)

	// Start the fixture.
	c, informers, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}
	stopCh := make(chan struct{})
	defer close(stopCh)
	informers.Start(stopCh)

	rsList, err := c.getReplicaSetsForDeployment(d1)
	if err != nil {
		t.Fatalf("getReplicaSetsForDeployment() error: %v", err)
	}
	rsNames := []string{}
	for _, rs := range rsList {
		rsNames = append(rsNames, rs.Name)
	}
	if len(rsNames) != 1 || rsNames[0] != rs1.Name {
		t.Errorf("getReplicaSetsForDeployment() = %v, want [%v]", rsNames, rs1.Name)
	}

	rsList, err = c.getReplicaSetsForDeployment(d2)
	if err != nil {
		t.Fatalf("getReplicaSetsForDeployment() error: %v", err)
	}
	rsNames = []string{}
	for _, rs := range rsList {
		rsNames = append(rsNames, rs.Name)
	}
	if len(rsNames) != 1 || rsNames[0] != rs2.Name {
		t.Errorf("getReplicaSetsForDeployment() = %v, want [%v]", rsNames, rs2.Name)
	}
}

func TestGetReplicaSetsForDeploymentAdoptRelease(t *testing.T) {
	testGetReplicaSetsForDeploymentAdoptRelease(t, metav1.TenantDefault)
}

func TestGetReplicaSetsForDeploymentAdoptReleaseWithMultiTenancy(t *testing.T) {
	testGetReplicaSetsForDeploymentAdoptRelease(t, "test-te")
}

func testGetReplicaSetsForDeploymentAdoptRelease(t *testing.T, tenant string) {
	f := newFixture(t)
	d := newDeployment("foo", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)

	// RS with matching labels, but orphaned. Should be adopted and returned.
	rsAdopt := newReplicaSet(d, "rsAdopt", 1, tenant)
	rsAdopt.OwnerReferences = nil
	// RS with matching ControllerRef, but wrong labels. Should be released.
	rsRelease := newReplicaSet(d, "rsRelease", 1, tenant)
	rsRelease.Labels = map[string]string{"foo": "notbar"}
	f.dLister = append(f.dLister, d)
	f.rsLister = append(f.rsLister, rsAdopt, rsRelease)
	f.objects = append(f.objects, d, rsAdopt, rsRelease)

	// Start the fixture.
	c, informers, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}
	stopCh := make(chan struct{})
	defer close(stopCh)
	informers.Start(stopCh)

	rsList, err := c.getReplicaSetsForDeployment(d)
	if err != nil {
		t.Fatalf("getReplicaSetsForDeployment() error: %v", err)
	}
	rsNames := []string{}
	for _, rs := range rsList {
		rsNames = append(rsNames, rs.Name)
	}
	if len(rsNames) != 1 || rsNames[0] != rsAdopt.Name {
		t.Errorf("getReplicaSetsForDeployment() = %v, want [%v]", rsNames, rsAdopt.Name)
	}
}

func TestGetPodMapForReplicaSets(t *testing.T) {
	testGetPodMapForReplicaSets(t, metav1.TenantDefault)
}

func TestGetPodMapForReplicaSetsWithMultiTenancy(t *testing.T) {
	testGetPodMapForReplicaSets(t, "test-te")
}

func testGetPodMapForReplicaSets(t *testing.T, tenant string) {
	f := newFixture(t)
	d := newDeployment("foo", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)

	rs1 := newReplicaSet(d, "rs1", 1, tenant)
	rs2 := newReplicaSet(d, "rs2", 1, tenant)

	// Add a Pod for each ReplicaSet.
	pod1 := generatePodFromRS(rs1)
	pod2 := generatePodFromRS(rs2)
	// Add a Pod that has matching labels, but no ControllerRef.
	pod3 := generatePodFromRS(rs1)
	pod3.Name = "pod3"
	pod3.OwnerReferences = nil
	// Add a Pod that has matching labels and ControllerRef, but is inactive.
	pod4 := generatePodFromRS(rs1)
	pod4.Name = "pod4"
	pod4.Status.Phase = v1.PodFailed

	f.dLister = append(f.dLister, d)
	f.rsLister = append(f.rsLister, rs1, rs2)
	f.podLister = append(f.podLister, pod1, pod2, pod3, pod4)
	f.objects = append(f.objects, d, rs1, rs2, pod1, pod2, pod3, pod4)

	// Start the fixture.
	c, informers, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}
	stopCh := make(chan struct{})
	defer close(stopCh)
	informers.Start(stopCh)

	podMap, err := c.getPodMapForDeployment(d, f.rsLister)
	if err != nil {
		t.Fatalf("getPodMapForDeployment() error: %v", err)
	}
	podCount := 0
	for _, podList := range podMap {
		podCount += len(podList.Items)
	}
	if got, want := podCount, 3; got != want {
		t.Errorf("podCount = %v, want %v", got, want)
	}

	if got, want := len(podMap), 2; got != want {
		t.Errorf("len(podMap) = %v, want %v", got, want)
	}
	if got, want := len(podMap[rs1.UID].Items), 2; got != want {
		t.Errorf("len(podMap[rs1]) = %v, want %v", got, want)
	}
	expect := map[string]struct{}{"rs1-pod": {}, "pod4": {}}
	for _, pod := range podMap[rs1.UID].Items {
		if _, ok := expect[pod.Name]; !ok {
			t.Errorf("unexpected pod name for rs1: %s", pod.Name)
		}
	}
	if got, want := len(podMap[rs2.UID].Items), 1; got != want {
		t.Errorf("len(podMap[rs2]) = %v, want %v", got, want)
	}
	if got, want := podMap[rs2.UID].Items[0].Name, "rs2-pod"; got != want {
		t.Errorf("podMap[rs2] = [%v], want [%v]", got, want)
	}
}

func TestAddReplicaSet(t *testing.T) {
	testAddReplicaSet(t, metav1.TenantDefault)
}

func TestAddReplicaSetWithMultiTenancy(t *testing.T) {
	testAddReplicaSet(t, "test-te")
}

func testAddReplicaSet(t *testing.T, tenant string) {
	f := newFixture(t)

	d1 := newDeployment("d1", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d2 := newDeployment("d2", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)

	// Two ReplicaSets that match labels for both Deployments,
	// but have ControllerRefs to make ownership explicit.
	rs1 := newReplicaSet(d1, "rs1", 1, tenant)
	rs2 := newReplicaSet(d2, "rs2", 1, tenant)

	f.dLister = append(f.dLister, d1, d2)
	f.objects = append(f.objects, d1, d2, rs1, rs2)

	// Create the fixture but don't start it,
	// so nothing happens in the background.
	dc, _, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}

	dc.addReplicaSet(rs1)
	if got, want := dc.queue.Len(), 1; got != want {
		t.Fatalf("queue.Len() = %v, want %v", got, want)
	}
	key, done := dc.queue.Get()
	if key == nil || done {
		t.Fatalf("failed to enqueue controller for rs %v", rs1.Name)
	}
	expectedKey, _ := controller.KeyFunc(d1)
	if got, want := key.(string), expectedKey; got != want {
		t.Errorf("queue.Get() = %v, want %v", got, want)
	}

	dc.addReplicaSet(rs2)
	if got, want := dc.queue.Len(), 1; got != want {
		t.Fatalf("queue.Len() = %v, want %v", got, want)
	}
	key, done = dc.queue.Get()
	if key == nil || done {
		t.Fatalf("failed to enqueue controller for rs %v", rs2.Name)
	}
	expectedKey, _ = controller.KeyFunc(d2)
	if got, want := key.(string), expectedKey; got != want {
		t.Errorf("queue.Get() = %v, want %v", got, want)
	}
}

func TestAddReplicaSetOrphan(t *testing.T) {
	testAddReplicaSetOrphan(t, metav1.TenantDefault)
}

func TestAddReplicaSetOrphanWithMultiTenancy(t *testing.T) {
	testAddReplicaSetOrphan(t, "test-te")
}

func testAddReplicaSetOrphan(t *testing.T, tenant string) {
	f := newFixture(t)

	// 2 will match the RS, 1 won't.
	d1 := newDeployment("d1", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d2 := newDeployment("d2", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d3 := newDeployment("d3", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d3.Spec.Selector.MatchLabels = map[string]string{"foo": "notbar"}

	// Make the RS an orphan. Expect matching Deployments to be queued.
	rs := newReplicaSet(d1, "rs1", 1, tenant)
	rs.OwnerReferences = nil

	f.dLister = append(f.dLister, d1, d2, d3)
	f.objects = append(f.objects, d1, d2, d3)

	// Create the fixture but don't start it,
	// so nothing happens in the background.
	dc, _, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}

	dc.addReplicaSet(rs)
	if got, want := dc.queue.Len(), 2; got != want {
		t.Fatalf("queue.Len() = %v, want %v", got, want)
	}
}

func TestUpdateReplicaSet(t *testing.T) {
	testUpdateReplicaSet(t, metav1.TenantDefault)
}

func TestUpdateReplicaSetWithMultiTenancy(t *testing.T) {
	testUpdateReplicaSet(t, "test-te")
}

func testUpdateReplicaSet(t *testing.T, tenant string) {
	f := newFixture(t)

	d1 := newDeployment("d1", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d2 := newDeployment("d2", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)

	// Two ReplicaSets that match labels for both Deployments,
	// but have ControllerRefs to make ownership explicit.
	rs1 := newReplicaSet(d1, "rs1", 1, tenant)
	rs2 := newReplicaSet(d2, "rs2", 1, tenant)

	f.dLister = append(f.dLister, d1, d2)
	f.rsLister = append(f.rsLister, rs1, rs2)
	f.objects = append(f.objects, d1, d2, rs1, rs2)

	// Create the fixture but don't start it,
	// so nothing happens in the background.
	dc, _, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}

	prev := *rs1
	next := *rs1
	bumpResourceVersion(&next)
	dc.updateReplicaSet(&prev, &next)
	if got, want := dc.queue.Len(), 1; got != want {
		t.Fatalf("queue.Len() = %v, want %v", got, want)
	}
	key, done := dc.queue.Get()
	if key == nil || done {
		t.Fatalf("failed to enqueue controller for rs %v", rs1.Name)
	}
	expectedKey, _ := controller.KeyFunc(d1)
	if got, want := key.(string), expectedKey; got != want {
		t.Errorf("queue.Get() = %v, want %v", got, want)
	}

	prev = *rs2
	next = *rs2
	bumpResourceVersion(&next)
	dc.updateReplicaSet(&prev, &next)
	if got, want := dc.queue.Len(), 1; got != want {
		t.Fatalf("queue.Len() = %v, want %v", got, want)
	}
	key, done = dc.queue.Get()
	if key == nil || done {
		t.Fatalf("failed to enqueue controller for rs %v", rs2.Name)
	}
	expectedKey, _ = controller.KeyFunc(d2)
	if got, want := key.(string), expectedKey; got != want {
		t.Errorf("queue.Get() = %v, want %v", got, want)
	}
}

func TestUpdateReplicaSetOrphanWithNewLabels(t *testing.T) {
	testUpdateReplicaSetOrphanWithNewLabels(t, metav1.TenantDefault)
}

func TestUpdateReplicaSetOrphanWithNewLabelsWithMultiTenancy(t *testing.T) {
	testUpdateReplicaSetOrphanWithNewLabels(t, "test-te")
}

func testUpdateReplicaSetOrphanWithNewLabels(t *testing.T, tenant string) {
	f := newFixture(t)

	d1 := newDeployment("d1", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d2 := newDeployment("d2", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)

	// RS matches both, but is an orphan.
	rs := newReplicaSet(d1, "rs1", 1, tenant)
	rs.OwnerReferences = nil
	f.dLister = append(f.dLister, d1, d2)
	f.rsLister = append(f.rsLister, rs)
	f.objects = append(f.objects, d1, d2, rs)

	// Create the fixture but don't start it,
	// so nothing happens in the background.
	dc, _, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}

	// Change labels and expect all matching controllers to queue.
	prev := *rs
	prev.Labels = map[string]string{"foo": "notbar"}
	next := *rs
	bumpResourceVersion(&next)
	dc.updateReplicaSet(&prev, &next)
	if got, want := dc.queue.Len(), 2; got != want {
		t.Fatalf("queue.Len() = %v, want %v", got, want)
	}
}

func TestUpdateReplicaSetChangeControllerRef(t *testing.T) {
	testUpdateReplicaSetChangeControllerRef(t, metav1.TenantDefault)
}

func TestUpdateReplicaSetChangeControllerRefWithMultiTenancy(t *testing.T) {
	testUpdateReplicaSetChangeControllerRef(t, "test-te")
}

func testUpdateReplicaSetChangeControllerRef(t *testing.T, tenant string) {
	f := newFixture(t)
	d1 := newDeployment("d1", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d2 := newDeployment("d2", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)

	rs := newReplicaSet(d1, "rs1", 1, tenant)
	f.dLister = append(f.dLister, d1, d2)
	f.rsLister = append(f.rsLister, rs)
	f.objects = append(f.objects, d1, d2, rs)

	// Create the fixture but don't start it,
	// so nothing happens in the background.
	dc, _, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}

	// Change ControllerRef and expect both old and new to queue.
	prev := *rs
	prev.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(d2, controllerKind)}
	next := *rs
	bumpResourceVersion(&next)
	dc.updateReplicaSet(&prev, &next)
	if got, want := dc.queue.Len(), 2; got != want {
		t.Fatalf("queue.Len() = %v, want %v", got, want)
	}
}

func TestUpdateReplicaSetRelease(t *testing.T) {
	testUpdateReplicaSetRelease(t, metav1.TenantDefault)
}

func TestUpdateReplicaSetReleaseWithMultiTenancy(t *testing.T) {
	testUpdateReplicaSetRelease(t, "test-te")
}

func testUpdateReplicaSetRelease(t *testing.T, tenant string) {
	f := newFixture(t)
	d1 := newDeployment("d1", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d2 := newDeployment("d2", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)

	rs := newReplicaSet(d1, "rs1", 1, tenant)
	f.dLister = append(f.dLister, d1, d2)
	f.rsLister = append(f.rsLister, rs)
	f.objects = append(f.objects, d1, d2, rs)

	// Create the fixture but don't start it,
	// so nothing happens in the background.
	dc, _, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}

	// Remove ControllerRef and expect all matching controller to sync orphan.
	prev := *rs
	next := *rs
	next.OwnerReferences = nil
	bumpResourceVersion(&next)
	dc.updateReplicaSet(&prev, &next)
	if got, want := dc.queue.Len(), 2; got != want {
		t.Fatalf("queue.Len() = %v, want %v", got, want)
	}
}

func TestDeleteReplicaSet(t *testing.T) {
	testDeleteReplicaSet(t, metav1.TenantDefault)
}

func TestDeleteReplicaSetWithMultiTenancy(t *testing.T) {
	testDeleteReplicaSet(t, "test-te")
}

func testDeleteReplicaSet(t *testing.T, tenant string) {
	f := newFixture(t)
	d1 := newDeployment("d1", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d2 := newDeployment("d2", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)

	// Two ReplicaSets that match labels for both Deployments,
	// but have ControllerRefs to make ownership explicit.
	rs1 := newReplicaSet(d1, "rs1", 1, tenant)
	rs2 := newReplicaSet(d2, "rs2", 1, tenant)

	f.dLister = append(f.dLister, d1, d2)
	f.rsLister = append(f.rsLister, rs1, rs2)
	f.objects = append(f.objects, d1, d2, rs1, rs2)

	// Create the fixture but don't start it,
	// so nothing happens in the background.
	dc, _, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}

	dc.deleteReplicaSet(rs1)
	if got, want := dc.queue.Len(), 1; got != want {
		t.Fatalf("queue.Len() = %v, want %v", got, want)
	}
	key, done := dc.queue.Get()
	if key == nil || done {
		t.Fatalf("failed to enqueue controller for rs %v", rs1.Name)
	}
	expectedKey, _ := controller.KeyFunc(d1)
	if got, want := key.(string), expectedKey; got != want {
		t.Errorf("queue.Get() = %v, want %v", got, want)
	}

	dc.deleteReplicaSet(rs2)
	if got, want := dc.queue.Len(), 1; got != want {
		t.Fatalf("queue.Len() = %v, want %v", got, want)
	}
	key, done = dc.queue.Get()
	if key == nil || done {
		t.Fatalf("failed to enqueue controller for rs %v", rs2.Name)
	}
	expectedKey, _ = controller.KeyFunc(d2)
	if got, want := key.(string), expectedKey; got != want {
		t.Errorf("queue.Get() = %v, want %v", got, want)
	}
}

func TestDeleteReplicaSetOrphan(t *testing.T) {
	testDeleteReplicaSetOrphan(t, metav1.TenantDefault)
}

func TestDeleteReplicaSetOrphanWithMultiTenancy(t *testing.T) {
	testDeleteReplicaSetOrphan(t, "test-te")
}

func testDeleteReplicaSetOrphan(t *testing.T, tenant string) {
	f := newFixture(t)
	d1 := newDeployment("d1", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)
	d2 := newDeployment("d2", 1, nil, nil, nil, map[string]string{"foo": "bar"}, tenant)

	// Make the RS an orphan. Expect matching Deployments to be queued.
	rs := newReplicaSet(d1, "rs1", 1, tenant)
	rs.OwnerReferences = nil

	f.dLister = append(f.dLister, d1, d2)
	f.rsLister = append(f.rsLister, rs)
	f.objects = append(f.objects, d1, d2, rs)

	// Create the fixture but don't start it,
	// so nothing happens in the background.
	dc, _, err := f.newController()
	if err != nil {
		t.Fatalf("error creating Deployment controller: %v", err)
	}

	dc.deleteReplicaSet(rs)
	if got, want := dc.queue.Len(), 0; got != want {
		t.Fatalf("queue.Len() = %v, want %v", got, want)
	}
}

func bumpResourceVersion(obj metav1.Object) {
	ver, _ := strconv.ParseInt(obj.GetResourceVersion(), 10, 32)
	obj.SetResourceVersion(strconv.FormatInt(ver+1, 10))
}

// generatePodFromRS creates a pod, with the input ReplicaSet's selector and its template
func generatePodFromRS(rs *apps.ReplicaSet) *v1.Pod {
	trueVar := true
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rs.Name + "-pod",
			Namespace: rs.Namespace,
			Tenant:    rs.Tenant,
			Labels:    rs.Spec.Selector.MatchLabels,
			OwnerReferences: []metav1.OwnerReference{
				{UID: rs.UID, APIVersion: "v1beta1", Kind: "ReplicaSet", Name: rs.Name, Controller: &trueVar},
			},
		},
		Spec: rs.Spec.Template.Spec,
	}
}