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
	"fmt"

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/labelpatch"
	"github.com/openkruise/rollouts/pkg/util"
)

type realController struct {
	*util.WorkloadInfo
	client client.Client
	pods   []*corev1.Pod
	key    types.NamespacedName
	gvk    schema.GroupVersionKind
	object *apps.DaemonSet
}

func NewController(cli client.Client, key types.NamespacedName, gvk schema.GroupVersionKind) partitionstyle.Interface {
	return &realController{
		key:    key,
		client: cli,
		gvk:    gvk,
	}
}

// GetWorkloadInfo return workload information.
func (rc *realController) GetWorkloadInfo() *util.WorkloadInfo {
	return rc.WorkloadInfo
}

// BuildController will get workload object and parse workload info,
// and return an initialized controller for workload.
func (rc *realController) BuildController() (partitionstyle.Interface, error) {
	if rc.object != nil {
		return rc, nil
	}
	object := &apps.DaemonSet{}
	if err := rc.client.Get(context.TODO(), rc.key, object); err != nil {
		return rc, err
	}
	rc.object = object

	// Parse workload info for native DaemonSet
	rc.WorkloadInfo = util.ParseWorkload(object)

	// For native DaemonSet which has no updatedReadyReplicas field, we should
	// list and count its owned Pods one by one.
	if rc.WorkloadInfo != nil && rc.WorkloadInfo.Status.UpdatedReadyReplicas <= 0 {
		pods, err := rc.ListOwnedPods()
		if err != nil {
			return nil, err
		}
		updatedReadyReplicas := util.WrappedPodCount(pods, func(pod *corev1.Pod) bool {
			if !pod.DeletionTimestamp.IsZero() {
				return false
			}
			if !util.IsConsistentWithRevision(pod.GetLabels(), rc.WorkloadInfo.Status.UpdateRevision) {
				return false
			}
			return util.IsPodReady(pod)
		})
		rc.WorkloadInfo.Status.UpdatedReadyReplicas = int32(updatedReadyReplicas)
	}
	return rc, nil
}

// ListOwnedPods fetch the pods owned by the workload.
// Note that we should list pod only if we really need it.
func (rc *realController) ListOwnedPods() ([]*corev1.Pod, error) {
	if rc.pods != nil {
		return rc.pods, nil
	}
	var err error
	rc.pods, err = util.ListOwnedPods(rc.client, rc.object)
	return rc.pods, err
}

// Initialize prepares the native DaemonSet for batch release by setting the appropriate update strategy.
// For native DaemonSet, we'll use OnDelete strategy to enable manual pod deletion for batch control.
func (rc *realController) Initialize(release *v1beta1.BatchRelease) error {
	if control.IsControlledByBatchRelease(release, rc.object) {
		return nil
	}

	// For native DaemonSet, we set the update strategy to OnDelete to enable manual control
	daemon := util.GetEmptyObjectWithKey(rc.object)
	owner := control.BuildReleaseControlInfo(release)

	// Set update strategy to OnDelete and add control annotations
	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}},"spec":{"updateStrategy":{"type":"OnDelete"}}}`,
		util.BatchReleaseControlAnnotation, owner)

	return rc.client.Patch(context.TODO(), daemon, client.RawPatch(types.MergePatchType, []byte(body)))
}

// UpgradeBatch handles the batch upgrade for native DaemonSet by deleting pods in the current batch.
// Since native DaemonSet uses OnDelete strategy, we need to manually delete pods to trigger updates.
func (rc *realController) UpgradeBatch(ctx *batchcontext.BatchContext) error {
	// For native DaemonSet with OnDelete strategy, we delete pods to trigger updates
	// Calculate how many pods need to be deleted based on the desired partition

	// Get the current number of updated pods
	currentUpdated := rc.Status.UpdatedReplicas

	// Calculate desired number of updated pods for this batch
	desiredUpdated := ctx.DesiredUpdatedReplicas

	// If we already have enough updated pods, no need to delete more
	if currentUpdated >= desiredUpdated {
		return nil
	}

	// Calculate how many pods we need to delete to reach desired state
	needToDelete := desiredUpdated - currentUpdated

	// Respect maxUnavailable setting from user
	maxUnavailable := int32(1) // Default value
	if rc.object.Spec.UpdateStrategy.RollingUpdate != nil && rc.object.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable != nil {
		maxUnavailableValue, err := intstr.GetScaledValueFromIntOrPercent(rc.object.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable, int(rc.Replicas), false)
		if err == nil && maxUnavailableValue > 0 {
			maxUnavailable = int32(maxUnavailableValue)
		}
	}

	// Limit the number of pods to delete based on maxUnavailable
	if needToDelete > maxUnavailable {
		needToDelete = maxUnavailable
	}

	// List pods that are not yet updated (still on old revision)
	var podsToDelete []*corev1.Pod
	for _, pod := range ctx.Pods {
		// Skip if pod is already marked for deletion
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}

		// Skip if pod is already on the update revision
		if util.IsConsistentWithRevision(pod.GetLabels(), ctx.UpdateRevision) {
			continue
		}

		// Add to deletion list
		podsToDelete = append(podsToDelete, pod)

		// Stop when we have enough pods to delete
		if int32(len(podsToDelete)) >= needToDelete {
			break
		}
	}

	// Delete the required number of pods
	for _, pod := range podsToDelete {
		err := rc.client.Delete(context.TODO(), pod)
		if err != nil {
			return fmt.Errorf("failed to delete pod %s/%s: %v", pod.Namespace, pod.Name, err)
		}
	}

	return nil
}

// Finalize cleans up the annotations and restores the original update strategy.
func (rc *realController) Finalize(release *v1beta1.BatchRelease) error {
	if rc.object == nil {
		return nil
	}

	var specBody string
	// if batchPartition == nil, workload should be promoted to use RollingUpdate
	if release.Spec.ReleasePlan.BatchPartition == nil {
		specBody = `,"spec":{"updateStrategy":{"type":"RollingUpdate"}}`
	}

	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}%s}`, util.BatchReleaseControlAnnotation, specBody)

	daemon := util.GetEmptyObjectWithKey(rc.object)
	return rc.client.Patch(context.TODO(), daemon, client.RawPatch(types.MergePatchType, []byte(body)))
}

// CalculateBatchContext calculates the batch context for native DaemonSet.
func (rc *realController) CalculateBatchContext(release *v1beta1.BatchRelease) (*batchcontext.BatchContext, error) {
	rolloutID := release.Spec.ReleasePlan.RolloutID
	if rolloutID != "" {
		// if rollout-id is set, the pod will be patched batch label,
		// so we have to list pod here.
		if _, err := rc.ListOwnedPods(); err != nil {
			return nil, err
		}
	}

	// current batch index
	currentBatch := release.Status.CanaryStatus.CurrentBatch
	// the number of no need update pods that marked before rollout
	noNeedUpdate := release.Status.CanaryStatus.NoNeedUpdateReplicas
	// the number of upgraded pods according to release plan in current batch.
	plannedUpdate := int32(control.CalculateBatchReplicas(release, int(rc.Replicas), int(currentBatch)))
	// the number of pods that should be upgraded in real
	desiredUpdate := plannedUpdate
	// the number of pods that should not be upgraded in real
	desiredStable := rc.Replicas - desiredUpdate
	// if we should consider the no-need-update pods that were marked before progressing
	if noNeedUpdate != nil && *noNeedUpdate > 0 {
		// specially, we should ignore the pods that were marked as no-need-update, this logic is for Rollback scene
		desiredUpdateNew := int32(control.CalculateBatchReplicas(release, int(rc.Replicas-*noNeedUpdate), int(currentBatch)))
		desiredStable = rc.Replicas - *noNeedUpdate - desiredUpdateNew
		desiredUpdate = rc.Replicas - desiredStable
	}

	// For native DaemonSet, we calculate based on the number of pods to update
	// rather than partition (since OnDelete doesn't use partition)
	desiredPartition := intstr.FromInt(int(desiredStable))
	if desiredStable <= 0 {
		desiredPartition = intstr.FromInt(0)
	}

	currentPartition := intstr.FromInt(0)
	// Native DaemonSet doesn't use partition in the same way, but we'll set it for consistency

	batchContext := &batchcontext.BatchContext{
		Pods:                   rc.pods,
		RolloutID:              rolloutID,
		CurrentBatch:           currentBatch,
		UpdateRevision:         release.Status.UpdateRevision,
		DesiredPartition:       desiredPartition,
		CurrentPartition:       currentPartition,
		FailureThreshold:       release.Spec.ReleasePlan.FailureThreshold,
		Replicas:               rc.Replicas,
		UpdatedReplicas:        rc.Status.UpdatedReplicas,
		UpdatedReadyReplicas:   rc.Status.UpdatedReadyReplicas,
		NoNeedUpdatedReplicas:  noNeedUpdate,
		PlannedUpdatedReplicas: plannedUpdate,
		DesiredUpdatedReplicas: desiredUpdate,
	}

	if noNeedUpdate != nil {
		batchContext.FilterFunc = labelpatch.FilterPodsForUnorderedUpdate
	}
	return batchContext, nil
}
