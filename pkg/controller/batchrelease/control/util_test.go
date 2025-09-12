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

package control

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
)

func TestParseIntegerAsPercentage(t *testing.T) {
	RegisterFailHandler(Fail)

	supposeUpper := 10000
	for allReplicas := 1; allReplicas <= supposeUpper; allReplicas++ {
		for percent := 0; percent <= 100; percent++ {
			canaryPercent := intstr.FromString(fmt.Sprintf("%v%%", percent))
			canaryReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&canaryPercent, allReplicas, true)
			partition := ParseIntegerAsPercentageIfPossible(int32(allReplicas-canaryReplicas), int32(allReplicas), &canaryPercent)
			stableReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&partition, allReplicas, true)
			if percent == 0 {
				Expect(stableReplicas).Should(BeNumerically("==", allReplicas))
			} else if percent == 100 {
				Expect(stableReplicas).Should(BeNumerically("==", 0))
			} else if percent > 0 {
				Expect(allReplicas - stableReplicas).To(BeNumerically(">", 0))
			}
			Expect(stableReplicas).Should(BeNumerically("<=", allReplicas))
			Expect(math.Abs(float64((allReplicas - canaryReplicas) - stableReplicas))).Should(BeNumerically("<", float64(allReplicas)*0.01))
		}
	}
}

func TestCalculateBatchReplicas(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := map[string]struct {
		batchReplicas    intstr.IntOrString
		workloadReplicas int32
		expectedReplicas int32
	}{
		"batch: 5, replicas: 10": {
			batchReplicas:    intstr.FromInt(5),
			workloadReplicas: 10,
			expectedReplicas: 5,
		},
		"batch: 20%, replicas: 10": {
			batchReplicas:    intstr.FromString("20%"),
			workloadReplicas: 10,
			expectedReplicas: 2,
		},
		"batch: 100%, replicas: 10": {
			batchReplicas:    intstr.FromString("100%"),
			workloadReplicas: 10,
			expectedReplicas: 10,
		},
		"batch: 200%, replicas: 10": {
			batchReplicas:    intstr.FromString("200%"),
			workloadReplicas: 10,
			expectedReplicas: 10,
		},
		"batch: 200, replicas: 10": {
			batchReplicas:    intstr.FromInt(200),
			workloadReplicas: 10,
			expectedReplicas: 10,
		},
		"batch: 0, replicas: 10": {
			batchReplicas:    intstr.FromInt(0),
			workloadReplicas: 10,
			expectedReplicas: 0,
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			release := &v1beta1.BatchRelease{
				Spec: v1beta1.BatchReleaseSpec{
					ReleasePlan: v1beta1.ReleasePlan{
						Batches: []v1beta1.ReleaseBatch{
							{
								CanaryReplicas: cs.batchReplicas,
							},
						},
					},
				},
			}
			got := CalculateBatchReplicas(release, int(cs.workloadReplicas), 0)
			Expect(got).Should(BeNumerically("==", cs.expectedReplicas))
		})
	}
}

func TestIsControlledByBatchRelease(t *testing.T) {
	RegisterFailHandler(Fail)

	release := &v1beta1.BatchRelease{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rollouts.kruise.io/v1alpha1",
			Kind:       "BatchRelease",
		},
		ObjectMeta: metav1.ObjectMeta{
			UID:       "test",
			Name:      "test",
			Namespace: "test",
		},
	}

	controlInfo, _ := json.Marshal(metav1.NewControllerRef(release, release.GroupVersionKind()))

	cases := map[string]struct {
		object *apps.Deployment
		result bool
	}{
		"ownerRef": {
			object: &apps.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						*metav1.NewControllerRef(release, release.GroupVersionKind()),
					},
				},
			},
			result: true,
		},
		"annoRef": {
			object: &apps.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						util.BatchReleaseControlAnnotation: string(controlInfo),
					},
				},
			},
			result: true,
		},
		"notRef": {
			object: &apps.Deployment{},
			result: false,
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			got := IsControlledByBatchRelease(release, cs.object)
			Expect(got == cs.result).To(BeTrue())
		})
	}
}

func TestIsCurrentMoreThanOrEqualToDesired(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := map[string]struct {
		current intstr.IntOrString
		desired intstr.IntOrString
		result  bool
	}{
		"current=2,desired=1": {
			current: intstr.FromInt(2),
			desired: intstr.FromInt(1),
			result:  true,
		},
		"current=2,desired=2": {
			current: intstr.FromInt(2),
			desired: intstr.FromInt(2),
			result:  true,
		},
		"current=2,desired=3": {
			current: intstr.FromInt(2),
			desired: intstr.FromInt(3),
			result:  false,
		},
		"current=80%,desired=79%": {
			current: intstr.FromString("80%"),
			desired: intstr.FromString("79%"),
			result:  true,
		},
		"current=80%,desired=80%": {
			current: intstr.FromString("80%"),
			desired: intstr.FromString("80%"),
			result:  true,
		},
		"current=80%,desired=81%": {
			current: intstr.FromString("80%"),
			desired: intstr.FromString("81%"),
			result:  false,
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			got := IsCurrentMoreThanOrEqualToDesired(cs.current, cs.desired)
			Expect(got).Should(Equal(cs.result))
		})
	}
}

// Tests for native DaemonSet specialized functions
func TestGetOriginalDaemonSetSetting(t *testing.T) {
	RegisterFailHandler(Fail)

	// Test case 1: DaemonSet with no annotation
	daemonSetWithoutAnnotation := &apps.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-daemonset",
			Namespace:   "default",
			Annotations: map[string]string{},
		},
	}

	setting, err := GetOriginalDaemonSetSetting(daemonSetWithoutAnnotation)
	Expect(err).ShouldNot(HaveOccurred())
	Expect(setting).Should(Equal(OriginalDeploymentStrategy{}))

	// Test case 2: DaemonSet with valid annotation
	expectedSetting := OriginalDeploymentStrategy{
		MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
		MaxSurge:       &intstr.IntOrString{Type: intstr.String, StrVal: "10%"},
	}
	settingBytes, _ := json.Marshal(expectedSetting)
	daemonSetWithAnnotation := &apps.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-daemonset",
			Namespace: "default",
			Annotations: map[string]string{
				v1beta1.OriginalDeploymentStrategyAnnotation: string(settingBytes),
			},
		},
	}

	setting, err = GetOriginalDaemonSetSetting(daemonSetWithAnnotation)
	Expect(err).ShouldNot(HaveOccurred())
	Expect(*setting.MaxUnavailable).Should(Equal(*expectedSetting.MaxUnavailable))
	Expect(*setting.MaxSurge).Should(Equal(*expectedSetting.MaxSurge))
}

func TestInitOriginalDaemonSetSetting(t *testing.T) {
	RegisterFailHandler(Fail)

	// Test case 1: Unsupported object type should panic
	deployment := &apps.Deployment{}
	Expect(func() {
		setting := OriginalDeploymentStrategy{}
		InitOriginalDaemonSetSetting(&setting, deployment)
	}).Should(Panic())

	// Test case 2: DaemonSet with nil MaxUnavailable and MaxSurge should set defaults
	daemonSet := &apps.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-daemonset",
			Namespace: "default",
		},
		Spec: apps.DaemonSetSpec{
			UpdateStrategy: apps.DaemonSetUpdateStrategy{
				Type:          apps.RollingUpdateDaemonSetStrategyType,
				RollingUpdate: &apps.RollingUpdateDaemonSet{},
			},
		},
	}

	setting := OriginalDeploymentStrategy{}
	InitOriginalDaemonSetSetting(&setting, daemonSet)
	Expect(setting.MaxUnavailable).ShouldNot(BeNil())
	Expect(setting.MaxSurge).ShouldNot(BeNil())
	Expect(setting.MaxUnavailable.String()).Should(Equal("1"))
	Expect(setting.MaxSurge.String()).Should(Equal("0"))

	// Test case 3: DaemonSet with existing MaxUnavailable and MaxSurge should not overwrite
	existingMaxUnavailable := intstr.FromInt(3)
	existingMaxSurge := intstr.FromString("20%")
	setting = OriginalDeploymentStrategy{
		MaxUnavailable: &existingMaxUnavailable,
		MaxSurge:       &existingMaxSurge,
	}
	InitOriginalDaemonSetSetting(&setting, daemonSet)
	Expect(setting.MaxUnavailable.String()).Should(Equal("3"))
	Expect(setting.MaxSurge.String()).Should(Equal("20%"))

	// Test case 4: DaemonSet with specific MaxUnavailable and MaxSurge values
	specificMaxUnavailable := intstr.FromInt(2)
	specificMaxSurge := intstr.FromString("15%")
	daemonSetWithValues := &apps.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-daemonset",
			Namespace: "default",
		},
		Spec: apps.DaemonSetSpec{
			UpdateStrategy: apps.DaemonSetUpdateStrategy{
				Type: apps.RollingUpdateDaemonSetStrategyType,
				RollingUpdate: &apps.RollingUpdateDaemonSet{
					MaxUnavailable: &specificMaxUnavailable,
					MaxSurge:       &specificMaxSurge,
				},
			},
		},
	}

	setting = OriginalDeploymentStrategy{}
	InitOriginalDaemonSetSetting(&setting, daemonSetWithValues)
	Expect(setting.MaxUnavailable.String()).Should(Equal("2"))
	Expect(setting.MaxSurge.String()).Should(Equal("15%"))
}

func TestGetMaxSurgeFromNativeDaemonSet(t *testing.T) {
	RegisterFailHandler(Fail)

	// Test case 1: nil RollingUpdateDaemonSet should return default
	result := getMaxSurgeFromNativeDaemonSet(nil)
	Expect(result.String()).Should(Equal("0"))

	// Test case 2: RollingUpdateDaemonSet with nil MaxSurge should return default
	ru := &apps.RollingUpdateDaemonSet{}
	result = getMaxSurgeFromNativeDaemonSet(ru)
	Expect(result.String()).Should(Equal("0"))

	// Test case 3: RollingUpdateDaemonSet with MaxSurge should return the value
	maxSurge := intstr.FromString("10%")
	ru = &apps.RollingUpdateDaemonSet{
		MaxSurge: &maxSurge,
	}
	result = getMaxSurgeFromNativeDaemonSet(ru)
	Expect(result.String()).Should(Equal("10%"))
}

func TestGetMaxUnavailableFromNativeDaemonSet(t *testing.T) {
	RegisterFailHandler(Fail)

	// Test case 1: nil RollingUpdateDaemonSet should return default
	result := getMaxUnavailableFromNativeDaemonSet(nil)
	Expect(result.String()).Should(Equal("1"))

	// Test case 2: RollingUpdateDaemonSet with nil MaxUnavailable should return default
	ru := &apps.RollingUpdateDaemonSet{}
	result = getMaxUnavailableFromNativeDaemonSet(ru)
	Expect(result.String()).Should(Equal("1"))

	// Test case 3: RollingUpdateDaemonSet with MaxUnavailable should return the value
	maxUnavailable := intstr.FromString("20%")
	ru = &apps.RollingUpdateDaemonSet{
		MaxUnavailable: &maxUnavailable,
	}
	result = getMaxUnavailableFromNativeDaemonSet(ru)
	Expect(result.String()).Should(Equal("20%"))
}
