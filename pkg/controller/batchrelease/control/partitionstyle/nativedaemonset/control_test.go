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
	"testing"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
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

	ctx := &context.BatchContext{
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
				"app":                                "daemon-demo",
				apps.DefaultDeploymentUniqueLabelKey: "old-version",
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
				"app":                                "daemon-demo",
				apps.DefaultDeploymentUniqueLabelKey: "old-version",
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
				"app":                                "daemon-demo",
				apps.DefaultDeploymentUniqueLabelKey: "old-version",
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

	ctx := &context.BatchContext{
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
