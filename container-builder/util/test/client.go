/*
 * Copyright 2022 Red Hat, Inc. and/or its affiliates.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package test

import (
	"context"
	"fmt"
	"strings"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	clientscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/scale"
	fakescale "k8s.io/client-go/scale/fake"
	"k8s.io/client-go/testing"
	controller "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kiegroup/kogito-serverless-operator/container-builder/client"
)

// NewFakeClient ---.
func NewFakeClient(initObjs ...runtime.Object) client.Client {
	scheme := clientscheme.Scheme

	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(initObjs...).Build()

	clientset := fakeclientset.NewSimpleClientset(filterObjects(scheme, initObjs, func(gvk schema.GroupVersionKind) bool {
		return !strings.Contains(gvk.Group, "knative")
	})...)
	replicasCount := make(map[string]int32)
	fakescaleclient := fakescale.FakeScaleClient{}
	fakescaleclient.AddReactor("update", "*", func(rawAction testing.Action) (bool, runtime.Object, error) {
		action := rawAction.(testing.UpdateAction)       // nolint: forcetypeassert
		obj := action.GetObject().(*autoscalingv1.Scale) // nolint: forcetypeassert
		replicas := obj.Spec.Replicas
		key := fmt.Sprintf("%s:%s:%s/%s", action.GetResource().Group, action.GetResource().Resource, action.GetNamespace(), obj.GetName())
		replicasCount[key] = replicas
		return true, &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{
				Name:      obj.Name,
				Namespace: action.GetNamespace(),
			},
			Spec: autoscalingv1.ScaleSpec{
				Replicas: replicas,
			},
		}, nil
	})
	fakescaleclient.AddReactor("get", "*", func(rawAction testing.Action) (bool, runtime.Object, error) {
		action := rawAction.(testing.GetAction) // nolint: forcetypeassert
		key := fmt.Sprintf("%s:%s:%s/%s", action.GetResource().Group, action.GetResource().Resource, action.GetNamespace(), action.GetName())
		obj := &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{
				Name:      action.GetName(),
				Namespace: action.GetNamespace(),
			},
			Spec: autoscalingv1.ScaleSpec{
				Replicas: replicasCount[key],
			},
		}
		return true, obj, nil
	})

	return &FakeClient{
		Client:    c,
		Interface: clientset,
		scales:    &fakescaleclient,
	}
}

func filterObjects(scheme *runtime.Scheme, input []runtime.Object, filter func(gvk schema.GroupVersionKind) bool) []runtime.Object {
	var res []runtime.Object
	for _, obj := range input {
		kinds, _, _ := scheme.ObjectKinds(obj)
		for _, k := range kinds {
			if filter(k) {
				res = append(res, obj)
				break
			}
		}
	}
	return res
}

// FakeClient ---.
type FakeClient struct {
	controller.Client
	kubernetes.Interface
	scales *fakescale.FakeScaleClient
}

// GetScheme ---.
func (c *FakeClient) GetScheme() *runtime.Scheme {
	return clientscheme.Scheme
}

func (c *FakeClient) GetConfig() *rest.Config {
	return nil
}

func (c *FakeClient) GetCurrentNamespace(kubeConfig string) (string, error) {
	return "", nil
}

// Patch mimicks patch for server-side apply and simply creates the obj.
func (c *FakeClient) Patch(ctx context.Context, obj controller.Object, patch controller.Patch, opts ...controller.PatchOption) error {
	return c.Create(ctx, obj)
}

func (c *FakeClient) Discovery() discovery.DiscoveryInterface {
	return &FakeDiscovery{
		DiscoveryInterface: c.Interface.Discovery(),
	}
}

func (c *FakeClient) ScalesClient() (scale.ScalesGetter, error) {
	return c.scales, nil
}

type FakeDiscovery struct {
	discovery.DiscoveryInterface
}

func (f *FakeDiscovery) ServerResourcesForGroupVersion(groupVersion string) (*metav1.APIResourceList, error) {
	// Normalize the fake discovery to behave like the real implementation when checking for openshift
	if groupVersion == "image.openshift.io/v1" {
		return nil, k8serrors.NewNotFound(schema.GroupResource{
			Group: "image.openshift.io",
		}, "")
	}
	return f.DiscoveryInterface.ServerResourcesForGroupVersion(groupVersion)
}
