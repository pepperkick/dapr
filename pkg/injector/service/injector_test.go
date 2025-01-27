/*
Copyright 2023 The Dapr Authors
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

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"

	"github.com/dapr/dapr/pkg/injector/namespacednamematcher"
)

func TestConfigCorrectValues(t *testing.T) {
	i, err := NewInjector(Options{
		Config: Config{
			SidecarImage:                      "c",
			SidecarImagePullPolicy:            "d",
			Namespace:                         "e",
			AllowedServiceAccountsPrefixNames: "ns*:sa,namespace:sa*",
			ControlPlaneTrustDomain:           "trust.domain",
		},
	})
	assert.NoError(t, err)

	injector := i.(*injector)
	assert.Equal(t, "c", injector.config.SidecarImage)
	assert.Equal(t, "d", injector.config.SidecarImagePullPolicy)
	assert.Equal(t, "e", injector.config.Namespace)
	m, err := namespacednamematcher.CreateFromString("ns*:sa,namespace:sa*")
	assert.NoError(t, err)
	assert.Equal(t, m, injector.namespaceNameMatcher)
}

func TestNewInjectorBadAllowedPrefixedServiceAccountConfig(t *testing.T) {
	_, err := NewInjector(Options{
		Config: Config{
			SidecarImage:                      "c",
			SidecarImagePullPolicy:            "d",
			Namespace:                         "e",
			AllowedServiceAccountsPrefixNames: "ns*:sa,namespace:sa*sa",
		},
	})
	assert.Error(t, err)
}

func TestGetAppIDFromRequest(t *testing.T) {
	t.Run("can handle nil", func(t *testing.T) {
		appID := getAppIDFromRequest(nil)
		assert.Equal(t, "", appID)
	})

	t.Run("can handle empty admissionrequest object", func(t *testing.T) {
		fakeReq := &admissionv1.AdmissionRequest{}
		appID := getAppIDFromRequest(fakeReq)
		assert.Equal(t, "", appID)
	})

	t.Run("get appID from annotations", func(t *testing.T) {
		fakePod := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"dapr.io/app-id": "fakeID",
				},
			},
		}
		rawBytes, _ := json.Marshal(fakePod)
		fakeReq := &admissionv1.AdmissionRequest{
			Object: runtime.RawExtension{
				Raw: rawBytes,
			},
		}
		appID := getAppIDFromRequest(fakeReq)
		assert.Equal(t, "fakeID", appID)
	})

	t.Run("fall back to pod name", func(t *testing.T) {
		fakePod := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "mypod",
			},
		}
		rawBytes, _ := json.Marshal(fakePod)
		fakeReq := &admissionv1.AdmissionRequest{
			Object: runtime.RawExtension{
				Raw: rawBytes,
			},
		}
		appID := getAppIDFromRequest(fakeReq)
		assert.Equal(t, "mypod", appID)
	})
}

func TestAllowedControllersServiceAccountUID(t *testing.T) {
	client := kubernetesfake.NewSimpleClientset()

	testCases := []struct {
		namespace string
		name      string
	}{
		{metav1.NamespaceSystem, "replicaset-controller"},
		{"tekton-pipelines", "tekton-pipelines-controller"},
		{"test", "test"},
	}

	for _, testCase := range testCases {
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testCase.name,
				Namespace: testCase.namespace,
			},
		}
		_, err := client.CoreV1().ServiceAccounts(testCase.namespace).Create(context.TODO(), sa, metav1.CreateOptions{})
		assert.NoError(t, err)
	}

	t.Run("injector config has no allowed service account", func(t *testing.T) {
		uids, err := AllowedControllersServiceAccountUID(context.TODO(), Config{}, client)
		assert.NoError(t, err)
		assert.Equal(t, 2, len(uids))
	})

	t.Run("injector config has a valid allowed service account", func(t *testing.T) {
		uids, err := AllowedControllersServiceAccountUID(context.TODO(), Config{AllowedServiceAccounts: "test:test"}, client)
		assert.NoError(t, err)
		assert.Equal(t, 3, len(uids))
	})

	t.Run("injector config has a invalid allowed service account", func(t *testing.T) {
		uids, err := AllowedControllersServiceAccountUID(context.TODO(), Config{AllowedServiceAccounts: "abc:abc"}, client)
		assert.NoError(t, err)
		assert.Equal(t, 2, len(uids))
	})

	t.Run("injector config has multiple allowed service accounts", func(t *testing.T) {
		uids, err := AllowedControllersServiceAccountUID(context.TODO(), Config{AllowedServiceAccounts: "test:test,abc:abc"}, client)
		assert.NoError(t, err)
		assert.Equal(t, 3, len(uids))
	})
}

func TestReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("if injector ready return nil", func(t *testing.T) {
		i := &injector{ready: make(chan struct{})}
		close(i.ready)
		assert.NoError(t, i.Ready(ctx))
	})

	t.Run("if not ready then should return timeout error if context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*10)
		defer cancel()
		i := &injector{ready: make(chan struct{})}
		assert.Error(t, i.Ready(ctx), errors.New("timed out waiting for injector to become ready"))
	})
}
