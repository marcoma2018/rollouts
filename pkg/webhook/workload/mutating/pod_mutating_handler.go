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

	admissionv1 "k8s.io/api/admission/v1"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/openkruise/rollouts/pkg/util"
)

// PodCreateHandler handles Pod creation for native DaemonSet rollout
type PodCreateHandler struct {
	Client  client.Client
	Decoder *admission.Decoder
}

// Handle handles pod creation events
func (h *PodCreateHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	// Only handle pod creation (CREATE operation)
	if req.Operation != admissionv1.Create {
		return admission.Allowed("")
	}

	pod := &corev1.Pod{}
	err := h.Decoder.Decode(req, pod)
	if err != nil {
		return admission.Errored(400, err)
	}

	// Check if this pod belongs to a DaemonSet under rollout
	daemonSet, isUnderRollout, err := h.getControllingDaemonSet(ctx, pod)
	if err != nil {
		klog.Errorf("Failed to get controlling DaemonSet for pod %s/%s: %v", pod.Namespace, pod.Name, err)
		return admission.Allowed("")
	}

	// If pod is not controlled by a DaemonSet under rollout, do nothing
	if !isUnderRollout {
		return admission.Allowed("")
	}

	// Get the rollout revision for this DaemonSet
	revision := util.ComputeHash(&daemonSet.Spec.Template, nil)

	// Add pod-template-hash label to identify this pod as being updated
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	// Use the same label key as Kubernetes uses for DaemonSets
	pod.Labels[apps.DefaultDeploymentUniqueLabelKey] = revision

	// Marshal the modified pod
	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(500, err)
	}

	klog.Infof("Added pod-template-hash label to pod %s/%s for DaemonSet %s/%s with revision %s",
		pod.Namespace, pod.Name, daemonSet.Namespace, daemonSet.Name, revision)

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

// getControllingDaemonSet checks if the pod is controlled by a DaemonSet under rollout
func (h *PodCreateHandler) getControllingDaemonSet(ctx context.Context, pod *corev1.Pod) (*apps.DaemonSet, bool, error) {
	// Get the controller owner reference
	controllerRef := metav1.GetControllerOf(pod)
	if controllerRef == nil {
		return nil, false, nil
	}

	// Check if the controller is a DaemonSet
	if controllerRef.Kind != "DaemonSet" {
		return nil, false, nil
	}

	// Get the DaemonSet
	daemonSet := &apps.DaemonSet{}
	err := h.Client.Get(ctx, client.ObjectKey{
		Namespace: pod.Namespace,
		Name:      controllerRef.Name,
	}, daemonSet)
	if err != nil {
		return nil, false, err
	}

	// Check if the DaemonSet is under rollout control
	_, isUnderRollout := daemonSet.Annotations[util.InRolloutProgressingAnnotation]
	return daemonSet, isUnderRollout, nil
}
