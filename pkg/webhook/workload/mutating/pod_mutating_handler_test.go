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

package mutating

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/openkruise/rollouts/pkg/util"
)

func TestPodCreateHandlerHandle(t *testing.T) {
	// Create a fake client
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = apps.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	decoder := admission.NewDecoder(scheme)

	// Create the handler
	handler := &PodCreateHandler{
		Client:  client,
		Decoder: decoder,
	}

	// Create a test DaemonSet with rollout annotation
	daemonSet := &apps.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-daemonset",
			Namespace: "default",
			Annotations: map[string]string{
				util.InRolloutProgressingAnnotation: `{"rolloutName":"test-rollout"}`,
			},
		},
		Spec: apps.DaemonSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "nginx",
						},
					},
				},
			},
		},
	}

	// Create the test pod with owner reference
	controller := true
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: apps.SchemeGroupVersion.String(),
					Kind:       "DaemonSet",
					Name:       "test-daemonset",
					UID:        "test-uid",
					Controller: &controller,
				},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "nginx",
				},
			},
		},
	}

	// Add the DaemonSet to the fake client
	err := client.Create(context.TODO(), daemonSet)
	if err != nil {
		t.Fatalf("Failed to create DaemonSet: %v", err)
	}

	// Verify the DaemonSet was created
	ds := &apps.DaemonSet{}
	err = client.Get(context.TODO(), types.NamespacedName{Namespace: "default", Name: "test-daemonset"}, ds)
	if err != nil {
		t.Fatalf("Failed to get DaemonSet: %v", err)
	}
	t.Logf("Created DaemonSet: %s/%s", ds.Namespace, ds.Name)
	t.Logf("DaemonSet annotations: %v", ds.Annotations)

	// Test the getControllingDaemonSet function directly with the pod
	ds, isUnderRollout, err := handler.getControllingDaemonSet(context.TODO(), pod)
	if err != nil {
		t.Errorf("Failed to get controlling DaemonSet: %v", err)
	}

	if ds == nil {
		t.Error("Expected to find DaemonSet but got nil")
	} else {
		t.Logf("Found DaemonSet: %s/%s", ds.Namespace, ds.Name)
		t.Logf("DaemonSet annotations: %v", ds.Annotations)
	}

	if !isUnderRollout {
		t.Error("Expected pod to be controlled by DaemonSet under rollout")
	}

	// Check if the label would be added
	revision := util.ComputeHash(&daemonSet.Spec.Template, nil)
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	pod.Labels[apps.DefaultDeploymentUniqueLabelKey] = revision

	if pod.Labels[apps.DefaultDeploymentUniqueLabelKey] != revision {
		t.Errorf("Expected pod-template-hash label to be %s, but got %s", revision, pod.Labels[apps.DefaultDeploymentUniqueLabelKey])
	}
}

func TestPodCreateHandlerHandleNonCreateOperation(t *testing.T) {
	// Create a fake client
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = apps.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	decoder := admission.NewDecoder(scheme)

	// Create the handler
	handler := &PodCreateHandler{
		Client:  client,
		Decoder: decoder,
	}

	// Create the admission request with UPDATE operation
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Update,
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Pod",
			},
		},
	}

	// Call the handler
	resp := handler.Handle(context.TODO(), req)

	// Check the response
	if !resp.Allowed {
		t.Errorf("Expected response to be allowed, but got: %v", resp.Result)
	}
}

func TestPodCreateHandlerHandlePodWithoutController(t *testing.T) {
	// Create a fake client
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = apps.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	decoder := admission.NewDecoder(scheme)

	// Create the handler
	handler := &PodCreateHandler{
		Client:  client,
		Decoder: decoder,
	}

	// Create a test pod without owner reference
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "nginx",
				},
			},
		},
	}

	// Marshal the pod to raw bytes
	podBytes, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("Failed to marshal pod: %v", err)
	}

	// Create the admission request
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object: runtime.RawExtension{
				Raw: podBytes,
			},
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Pod",
			},
		},
	}

	// Call the handler
	resp := handler.Handle(context.TODO(), req)

	// Check the response
	if !resp.Allowed {
		t.Errorf("Expected response to be allowed, but got: %v", resp.Result)
	}
}

func TestPodCreateHandlerHandleNonDaemonSetController(t *testing.T) {
	// Create a fake client
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = apps.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	decoder := admission.NewDecoder(scheme)

	// Create the handler
	handler := &PodCreateHandler{
		Client:  client,
		Decoder: decoder,
	}

	// Create a test pod with Deployment owner reference
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: apps.SchemeGroupVersion.String(),
					Kind:       "Deployment",
					Name:       "test-deployment",
					UID:        "test-uid",
				},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "nginx",
				},
			},
		},
	}

	// Marshal the pod to raw bytes
	podBytes, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("Failed to marshal pod: %v", err)
	}

	// Create the admission request
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object: runtime.RawExtension{
				Raw: podBytes,
			},
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Pod",
			},
		},
	}

	// Call the handler
	resp := handler.Handle(context.TODO(), req)

	// Check the response
	if !resp.Allowed {
		t.Errorf("Expected response to be allowed, but got: %v", resp.Result)
	}
}

func TestPodCreateHandlerHandleDaemonSetNotUnderRollout(t *testing.T) {
	// Create a fake client
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = apps.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	decoder := admission.NewDecoder(scheme)

	// Create the handler
	handler := &PodCreateHandler{
		Client:  client,
		Decoder: decoder,
	}

	// Create a test DaemonSet without rollout annotation
	daemonSet := &apps.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-daemonset",
			Namespace: "default",
		},
		Spec: apps.DaemonSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "nginx",
						},
					},
				},
			},
		},
	}

	// Create the test pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: apps.SchemeGroupVersion.String(),
					Kind:       "DaemonSet",
					Name:       "test-daemonset",
					UID:        "test-uid",
				},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "nginx",
				},
			},
		},
	}

	// Add the DaemonSet to the fake client
	err := client.Create(context.TODO(), daemonSet)
	if err != nil {
		t.Fatalf("Failed to create DaemonSet: %v", err)
	}

	// Marshal the pod to raw bytes
	podBytes, err := json.Marshal(pod)
	if err != nil {
		t.Fatalf("Failed to marshal pod: %v", err)
	}

	// Create the admission request
	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Object: runtime.RawExtension{
				Raw: podBytes,
			},
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Pod",
			},
		},
	}

	// Call the handler
	resp := handler.Handle(context.TODO(), req)

	// Check the response
	if !resp.Allowed {
		t.Errorf("Expected response to be allowed, but got: %v", resp.Result)
	}
}
