/*
Copyright 2022 The Kruise Authors.

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

package nativedaemonset

import (
	"context"
	"testing"

	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/stretchr/testify/assert"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	daemonDemo = &apps.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "DaemonSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "daemon-demo",
			Namespace: "default",
			Annotations: map[string]string{
				util.BatchReleaseControlAnnotation: `{"name":"release-demo","uid":"606132e0-85ef-460e-8a04-438496a92951","controller":true,"blockOwnerDeletion":true}`,
			},
		},
		Spec: apps.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "daemon-demo",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "daemon-demo",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "main",
							Image: "nginx:latest",
						},
					},
				},
			},
			UpdateStrategy: apps.DaemonSetUpdateStrategy{
				Type: apps.OnDeleteDaemonSetStrategyType,
				RollingUpdate: &apps.RollingUpdateDaemonSet{
					MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
				},
			},
		},
		Status: apps.DaemonSetStatus{
			CurrentNumberScheduled: 5,
			NumberMisscheduled:     0,
			DesiredNumberScheduled: 5,
			NumberReady:            5,
			UpdatedNumberScheduled: 0,
			NumberAvailable:        5,
		},
	}

	batchReleaseDemo = &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "release-demo",
			Namespace: "default",
			UID:       "606132e0-85ef-460e-8a04-438496a92951",
		},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: "apps/v1",
				Kind:       "DaemonSet",
				Name:       "daemon-demo",
			},
			ReleasePlan: v1beta1.ReleasePlan{
				Batches: []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromInt(1),
					},
					{
						CanaryReplicas: intstr.FromInt(3),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				},
			},
		},
		Status: v1beta1.BatchReleaseStatus{
			CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
				CurrentBatch: 0,
			},
		},
	}

	// Add a rollback batch release for testing rollback functionality
	batchReleaseRollbackDemo = &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "release-demo",
			Namespace: "default",
			UID:       "606132e0-85ef-460e-8a04-438496a92951",
			Annotations: map[string]string{
				"rollouts.kruise.io/rollback-in-batch": "true",
			},
		},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: "apps/v1",
				Kind:       "DaemonSet",
				Name:       "daemon-demo",
			},
			ReleasePlan: v1beta1.ReleasePlan{
				Batches: []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromInt(1),
					},
					{
						CanaryReplicas: intstr.FromInt(3),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				},
				RolloutID: "test-rollout-id",
			},
		},
		Status: v1beta1.BatchReleaseStatus{
			CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
				CurrentBatch:         0,
				NoNeedUpdateReplicas: pointer.Int32(2),
			},
			UpdateRevision: "update-version",
		},
	}
)

func TestNewController(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemonDemo).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	assert.NotNil(t, controller)
}

func TestBuildController(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemonDemo).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, err := controller.BuildController()
	assert.NoError(t, err)
	assert.NotNil(t, builtController)
}

func TestInitialize(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	err := builtController.Initialize(batchReleaseDemo)
	assert.NoError(t, err)
}

func TestUpgradeBatch(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	ctx := &batchcontext.BatchContext{
		DesiredUpdatedReplicas: 2,
		UpdateRevision:         "update-version",
		Pods:                   []*corev1.Pod{},
	}

	err := builtController.UpgradeBatch(ctx)
	assert.NoError(t, err)
}

func TestUpgradeBatchWithMaxUnavailable(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	// Set maxUnavailable to 2 for this test
	maxUnavailable := intstr.FromInt(2)
	daemon.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable = &maxUnavailable

	// Create some pods for testing
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app": "daemon-demo",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:latest",
				},
			},
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Labels: map[string]string{
				"app": "daemon-demo",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:latest",
				},
			},
		},
	}

	pod3 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-3",
			Namespace: "default",
			Labels: map[string]string{
				"app": "daemon-demo",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:latest",
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon, pod1, pod2, pod3).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Set up the pods in the controller
	pods := []*corev1.Pod{pod1, pod2, pod3}
	builtController.(*realController).pods = pods

	ctx := &batchcontext.BatchContext{
		DesiredUpdatedReplicas: 2,
		UpdateRevision:         "update-version",
		Pods:                   pods,
		Replicas:               5,
	}

	err := builtController.UpgradeBatch(ctx)
	assert.NoError(t, err)
}

func TestUpgradeBatchWithPodsWithTemplateHash(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	// Set maxUnavailable to 2 for this test
	maxUnavailable := intstr.FromInt(2)
	daemon.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable = &maxUnavailable

	// Create some pods for testing - some with pod-template-hash, some without
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app":               "daemon-demo",
				"pod-template-hash": "old-version",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:latest",
				},
			},
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Labels: map[string]string{
				"app": "daemon-demo",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:latest",
				},
			},
		},
	}

	pod3 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-3",
			Namespace: "default",
			Labels: map[string]string{
				"app": "daemon-demo",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:latest",
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon, pod1, pod2, pod3).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Set up the pods in the controller
	pods := []*corev1.Pod{pod1, pod2, pod3}
	builtController.(*realController).pods = pods

	ctx := &batchcontext.BatchContext{
		DesiredUpdatedReplicas: 2,
		UpdateRevision:         "update-version",
		Pods:                   pods,
		Replicas:               5,
	}

	err := builtController.UpgradeBatch(ctx)
	assert.NoError(t, err)
}

func TestUpgradeBatchWithPodsBeingDeleted(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()

	// Create some pods for testing - one being deleted
	now := metav1.Now()
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pod-1",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{"test-finalizer"},
			Labels: map[string]string{
				"app": "daemon-demo",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:latest",
				},
			},
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Labels: map[string]string{
				"app": "daemon-demo",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:latest",
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).WithObjects(pod1, pod2).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Set up the pods in the controller by listing them
	pods, _ := builtController.ListOwnedPods()
	builtController.(*realController).pods = pods

	ctx := &batchcontext.BatchContext{
		DesiredUpdatedReplicas: 2,
		UpdateRevision:         "update-version",
		Pods:                   pods,
		Replicas:               5,
	}

	err := builtController.UpgradeBatch(ctx)
	assert.NoError(t, err)
}

func TestFinalize(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	err := builtController.Finalize(batchReleaseDemo)
	assert.NoError(t, err)
}

func TestFinalizeWithPodTemplateHashCleanup(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	// Set to OnDelete strategy to simulate initialized state
	daemon.Spec.UpdateStrategy.Type = apps.OnDeleteDaemonSetStrategyType

	// Create pods with pod-template-hash labels
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app":               "daemon-demo",
				"pod-template-hash": "old-version",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:latest",
				},
			},
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Labels: map[string]string{
				"app": "daemon-demo",
				// No pod-template-hash label
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:latest",
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon, pod1, pod2).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Set up the pods in the controller
	pods := []*corev1.Pod{pod1, pod2}
	builtController.(*realController).pods = pods

	// Create a batch release with nil BatchPartition (indicating completion)
	completedBatchRelease := batchReleaseDemo.DeepCopy()
	completedBatchRelease.Spec.ReleasePlan.BatchPartition = nil

	err := builtController.Finalize(completedBatchRelease)
	assert.NoError(t, err)

	// Verify the pods were updated correctly
	updatedPod1 := &corev1.Pod{}
	err = cli.Get(context.TODO(), types.NamespacedName{Name: "pod-1", Namespace: "default"}, updatedPod1)
	assert.NoError(t, err)

	// Check that the pod-template-hash label was removed from pod1
	assert.NotContains(t, updatedPod1.Labels, "pod-template-hash")

	// Verify the DaemonSet was updated correctly
	updatedDaemon := &apps.DaemonSet{}
	err = cli.Get(context.TODO(), key, updatedDaemon)
	assert.NoError(t, err)

	// Check that the update strategy is restored to RollingUpdate
	assert.Equal(t, apps.RollingUpdateDaemonSetStrategyType, updatedDaemon.Spec.UpdateStrategy.Type)

	// Check that control annotation is removed
	assert.NotContains(t, updatedDaemon.Annotations, util.BatchReleaseControlAnnotation)
}

func TestCleanupPodTemplateHashLabels(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()

	// Create pods with pod-template-hash labels
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app":               "daemon-demo",
				"pod-template-hash": "old-version",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:latest",
				},
			},
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Labels: map[string]string{
				"app": "daemon-demo",
				// No pod-template-hash label
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:latest",
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon, pod1, pod2).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Set up the pods in the controller
	pods := []*corev1.Pod{pod1, pod2}
	builtController.(*realController).pods = pods

	// Call the cleanup function directly
	err := builtController.(*realController).cleanupPodTemplateHashLabels()
	assert.NoError(t, err)

	// Verify the pods were updated correctly
	updatedPod1 := &corev1.Pod{}
	err = cli.Get(context.TODO(), types.NamespacedName{Name: "pod-1", Namespace: "default"}, updatedPod1)
	assert.NoError(t, err)

	// Check that the pod-template-hash label was removed from pod1
	assert.NotContains(t, updatedPod1.Labels, "pod-template-hash")

	// Verify pod2 was not changed (it didn't have the label)
	updatedPod2 := &corev1.Pod{}
	err = cli.Get(context.TODO(), types.NamespacedName{Name: "pod-2", Namespace: "default"}, updatedPod2)
	assert.NoError(t, err)
	assert.NotContains(t, updatedPod2.Labels, "pod-template-hash")
}

func TestCleanupPodTemplateHashLabelsErrorHandling(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()

	// Create a pod with pod-template-hash label
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app":               "daemon-demo",
				"pod-template-hash": "old-version",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:latest",
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon, pod1).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Set up the pods in the controller
	pods := []*corev1.Pod{pod1}
	builtController.(*realController).pods = pods

	// Delete the pod to simulate a NotFound error
	cli.Delete(context.TODO(), pod1)

	// Call the cleanup function directly - should handle NotFound error gracefully
	err := builtController.(*realController).cleanupPodTemplateHashLabels()
	assert.NoError(t, err)
}

func TestCalculateBatchContext(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	ctx, err := builtController.CalculateBatchContext(batchReleaseDemo)
	assert.NoError(t, err)
	assert.NotNil(t, ctx)
}

func TestCalculateBatchContextWithRollback(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	ctx, err := builtController.CalculateBatchContext(batchReleaseRollbackDemo)
	assert.NoError(t, err)
	assert.NotNil(t, ctx)
	// With rollback, we should have NoNeedUpdatedReplicas set
	assert.NotNil(t, ctx.NoNeedUpdatedReplicas)
	assert.Equal(t, int32(2), *ctx.NoNeedUpdatedReplicas)
}

// Additional tests for native DaemonSet specialized functions
func TestInitializeWithOriginalStrategyAnnotation(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	err := builtController.Initialize(batchReleaseDemo)
	assert.NoError(t, err)

	// Verify the DaemonSet was updated correctly
	updatedDaemon := &apps.DaemonSet{}
	err = cli.Get(context.TODO(), key, updatedDaemon)
	assert.NoError(t, err)

	// Check that the update strategy is now OnDelete
	assert.Equal(t, apps.OnDeleteDaemonSetStrategyType, updatedDaemon.Spec.UpdateStrategy.Type)

	// Check that the control annotation is set
	assert.Contains(t, updatedDaemon.Annotations, util.BatchReleaseControlAnnotation)
}

func TestFinalizeWithBatchPartitionNil(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	// Set to OnDelete strategy to simulate initialized state
	daemon.Spec.UpdateStrategy.Type = apps.OnDeleteDaemonSetStrategyType

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Create a batch release with nil BatchPartition (indicating completion)
	completedBatchRelease := batchReleaseDemo.DeepCopy()
	completedBatchRelease.Spec.ReleasePlan.BatchPartition = nil

	err := builtController.Finalize(completedBatchRelease)
	assert.NoError(t, err)

	// Verify the DaemonSet was updated correctly
	updatedDaemon := &apps.DaemonSet{}
	err = cli.Get(context.TODO(), key, updatedDaemon)
	assert.NoError(t, err)

	// Check that the update strategy is restored to RollingUpdate
	assert.Equal(t, apps.RollingUpdateDaemonSetStrategyType, updatedDaemon.Spec.UpdateStrategy.Type)

	// Check that annotations are removed
	assert.NotContains(t, updatedDaemon.Annotations, util.BatchReleaseControlAnnotation)
}

func TestFinalizeWithBatchPartitionNotNil(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	// Set to OnDelete strategy to simulate initialized state
	daemon.Spec.UpdateStrategy.Type = apps.OnDeleteDaemonSetStrategyType

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Create a batch release with non-nil BatchPartition (indicating in-progress)
	inProgressBatchRelease := batchReleaseDemo.DeepCopy()
	batchPartition := int32(1)
	inProgressBatchRelease.Spec.ReleasePlan.BatchPartition = &batchPartition

	err := builtController.Finalize(inProgressBatchRelease)
	assert.NoError(t, err)

	// Verify the DaemonSet was updated correctly
	updatedDaemon := &apps.DaemonSet{}
	err = cli.Get(context.TODO(), key, updatedDaemon)
	assert.NoError(t, err)

	// Check that control annotation is removed
	assert.NotContains(t, updatedDaemon.Annotations, util.BatchReleaseControlAnnotation)
	// Update strategy should remain OnDelete since batch is not complete
	assert.Equal(t, apps.OnDeleteDaemonSetStrategyType, updatedDaemon.Spec.UpdateStrategy.Type)
}
