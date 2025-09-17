package nativedaemonset

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
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
			// Use rc.WorkloadInfo.Status.UpdateRevision for consistency
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
func (rc *realController) ListOwnedPods() ([]*corev1.Pod, error) {
	if rc.pods != nil {
		return rc.pods, nil
	}
	var err error
	rc.pods, err = util.ListOwnedPods(rc.client, rc.object)
	return rc.pods, err
}

// Initialize prepares the native DaemonSet for batch release by setting the appropriate update strategy.
func (rc *realController) Initialize(release *v1beta1.BatchRelease) error {
	if control.IsControlledByBatchRelease(release, rc.object) {
		return nil
	}

	// For native DaemonSet, we set the update strategy to OnDelete to enable manual control
	daemon := util.GetEmptyObjectWithKey(rc.object)
	owner := control.BuildReleaseControlInfo(release)

	unescapedOwner, err := strconv.Unquote(`"` + owner + `"`)
	if err != nil {
		return fmt.Errorf("failed to unescape owner info: %v", err)
	}

	// Create a proper JSON patch using map structure to avoid escaping issues
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				util.BatchReleaseControlAnnotation: unescapedOwner,
			},
		},
		"spec": map[string]interface{}{
			"updateStrategy": map[string]interface{}{
				"type": "OnDelete",
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %v", err)
	}

	return rc.client.Patch(context.TODO(), daemon, client.RawPatch(types.MergePatchType, patchBytes))
}

// UpgradeBatch handles the batch upgrade for native DaemonSet by deleting pods in the current batch.
func (rc *realController) UpgradeBatch(ctx *batchcontext.BatchContext) error {
	// Force refresh pods to get the latest state from API server
	latestPods, err := rc.ListOwnedPods()
	if err != nil {
		klog.Errorf("Failed to list owned pods: %v", err)
		return err
	}

	// Update the context with the latest pods
	ctx.Pods = latestPods

	// Step 1: Check if there are pods being deleted, if so, exit immediately
	podsBeingDeleted := int32(0)
	for _, pod := range ctx.Pods {
		if !pod.DeletionTimestamp.IsZero() {
			podsBeingDeleted++
		}
	}

	if podsBeingDeleted > 0 {
		klog.Infof("Found %d pods being deleted, skipping this reconcile", podsBeingDeleted)
		return nil
	}

	// Step 2: Check if the number of updated pods has reached the target
	updatedPods := int32(0)
	var podsToDelete []*corev1.Pod

	for _, pod := range ctx.Pods {
		// Check if pod has pod-template-hash label (indicating it's updated)
		if _, hasTemplateHash := pod.Labels["pod-template-hash"]; hasTemplateHash {
			updatedPods++
			klog.Infof("Pod %s/%s has pod-template-hash, counting as updated", pod.Namespace, pod.Name)
		} else {
			// Pods without pod-template-hash need to be deleted
			podsToDelete = append(podsToDelete, pod)
		}
	}

	if updatedPods >= ctx.DesiredUpdatedReplicas {
		klog.Infof("Already have enough updated pods: %d >= %d, skipping deletion", updatedPods, ctx.DesiredUpdatedReplicas)
		return nil
	}

	// Step 3: Calculate the number of pods to delete
	needToDelete := ctx.DesiredUpdatedReplicas - updatedPods

	klog.Infof("Current status - updatedPods: %d, desired: %d, needToDelete: %d, available: %d",
		updatedPods, ctx.DesiredUpdatedReplicas, needToDelete, len(podsToDelete))

	// Step 4: Apply maxUnavailable constraint
	maxUnavailable := int32(1) // Default value
	if rc.object.Spec.UpdateStrategy.RollingUpdate != nil && rc.object.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable != nil {
		maxUnavailableValue, err := intstr.GetScaledValueFromIntOrPercent(rc.object.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable, int(rc.Replicas), false)
		if err == nil && maxUnavailableValue > 0 {
			maxUnavailable = int32(maxUnavailableValue)
		}
	}

	// Ensure we don't exceed the maxUnavailable limit
	if needToDelete > maxUnavailable {
		needToDelete = maxUnavailable
		klog.Infof("Limited needToDelete from %d to %d due to maxUnavailable constraint",
			ctx.DesiredUpdatedReplicas-updatedPods, needToDelete)
	}

	// Limit deletion count to available pod count
	if needToDelete > int32(len(podsToDelete)) {
		needToDelete = int32(len(podsToDelete))
		klog.Infof("Limited needToDelete to available pods: %d", needToDelete)
	}

	if needToDelete <= 0 {
		klog.Infof("No pods need to be deleted")
		return nil
	}

	klog.Infof("Planning to delete %d pods (maxUnavailable: %d)", needToDelete, maxUnavailable)

	// Step 5: Execute deletion
	// Sort pods by creation timestamp to delete oldest first
	sort.Slice(podsToDelete, func(i, j int) bool {
		return podsToDelete[i].CreationTimestamp.Before(&podsToDelete[j].CreationTimestamp)
	})

	deletedCount := int32(0)
	for i := int32(0); i < needToDelete && i < int32(len(podsToDelete)); i++ {
		pod := podsToDelete[i]
		klog.Infof("About to delete pod %s/%s (created: %v)", pod.Namespace, pod.Name, pod.CreationTimestamp)

		err := rc.client.Delete(context.TODO(), pod)
		if err != nil {
			klog.Errorf("Failed to delete pod %s/%s: %v", pod.Namespace, pod.Name, err)
			return fmt.Errorf("failed to delete pod %s/%s: %v", pod.Namespace, pod.Name, err)
		}

		klog.Infof("Successfully deleted pod %s/%s", pod.Namespace, pod.Name)
		deletedCount++
	}
	time.Sleep(5 * time.Second) // Short delay to allow API server to process deletions

	klog.Infof("Deleted %d pods in this execution", deletedCount)
	return nil
}

// Finalize cleans up the annotations and restores the original update strategy.
// It also removes the "pod-template-hash" label from all pods controlled by the DaemonSet.
func (rc *realController) Finalize(release *v1beta1.BatchRelease) error {
	if rc.object == nil {
		return nil
	}

	var specBody string
	// if batchPartition == nil, workload should be promoted to use the original update strategy
	if release.Spec.ReleasePlan.BatchPartition == nil {
		updateStrategy := apps.DaemonSetUpdateStrategy{
			Type: apps.RollingUpdateDaemonSetStrategyType,
		}
		strategyBytes, _ := json.Marshal(updateStrategy)
		specBody = fmt.Sprintf(`,"spec":{"updateStrategy":%s}`, string(strategyBytes))
	}

	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":null},"labels":{"rollouts.kruise.io/stable-revision":null}}%s}`,
		util.BatchReleaseControlAnnotation,
		specBody)

	daemon := util.GetEmptyObjectWithKey(rc.object)
	if err := rc.client.Patch(context.TODO(), daemon, client.RawPatch(types.MergePatchType, []byte(body))); err != nil {
		return err
	}

	// Clean up "pod-template-hash" label from all pods controlled by this DaemonSet
	return rc.cleanupPodTemplateHashLabels()
}

// cleanupPodTemplateHashLabels removes the "pod-template-hash" label from all pods
// controlled by the DaemonSet
func (rc *realController) cleanupPodTemplateHashLabels() error {
	// List all pods owned by this DaemonSet
	pods, err := rc.ListOwnedPods()
	if err != nil {
		klog.Errorf("Failed to list owned pods: %v", err)
		return err
	}

	// Remove "pod-template-hash" label from each pod
	for _, pod := range pods {
		// Check if the pod has the "pod-template-hash" label
		if _, hasLabel := pod.Labels["pod-template-hash"]; !hasLabel {
			continue
		}

		// Create a patch to remove the label
		patch := fmt.Sprintf(`{"metadata":{"labels":{"pod-template-hash":null}}}`)
		podClone := util.GetEmptyObjectWithKey(pod)

		if err := rc.client.Patch(context.TODO(), podClone, client.RawPatch(types.MergePatchType, []byte(patch))); err != nil {
			// Log the error but continue with other pods
			klog.Errorf("Failed to remove pod-template-hash label from pod %s/%s: %v", pod.Namespace, pod.Name, err)
			// Only return error if it's not a NotFound error
			if !errors.IsNotFound(err) {
				return err
			}
		} else {
			klog.Infof("Successfully removed pod-template-hash label from pod %s/%s", pod.Namespace, pod.Name)
		}
	}

	return nil
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

	// compute updatedReadyReplicas by pods
	// because DaemonSet has no updatedReadyReplicas field in status
	// we have to calculate it by ourselves
	var updatedReadyReplicas int32
	if rc.pods != nil {
		for _, pod := range rc.pods {
			// Skip if pod is marked for deletion
			if !pod.DeletionTimestamp.IsZero() {
				continue
			}

			// Check if pod is on the update revision
			if util.IsConsistentWithRevision(pod.GetLabels(), release.Status.UpdateRevision) {
				if util.IsPodReady(pod) {
					updatedReadyReplicas++
				}
			}
		}
	} else {
		// Fallback to using status values if pods are not available
		updatedReadyReplicas = rc.Status.UpdatedReadyReplicas
	}

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
		UpdatedReadyReplicas:   updatedReadyReplicas,
		NoNeedUpdatedReplicas:  noNeedUpdate,
		PlannedUpdatedReplicas: plannedUpdate,
		DesiredUpdatedReplicas: desiredUpdate,
	}

	klog.Infof("BatchContext for DaemonSet %s/%s - Batch %d: updated=%d, ready=%d, desired=%d, pods=%d",
		rc.key.Namespace, rc.key.Name, currentBatch, rc.Status.UpdatedReplicas, updatedReadyReplicas, desiredUpdate, len(rc.pods))

	if noNeedUpdate != nil {
		batchContext.FilterFunc = labelpatch.FilterPodsForUnorderedUpdate
	}
	return batchContext, nil
}
