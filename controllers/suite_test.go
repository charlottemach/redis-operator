/*
Copyright 2021.

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

package controllers

import (
	"context"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/containersolutions/redis-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var cluster *v1alpha1.RedisCluster = CreateRedisCluster()

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})

}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	err = (&RedisClusterReconciler{
		Client: k8sClient,
		Scheme: k8sManager.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("RedisCluster"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		err = k8sManager.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred())
	}()

	Expect(k8sClient.Create(context.Background(), cluster)).Should(Succeed())

}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	Expect(k8sClient.Delete(context.Background(), cluster)).Should(Succeed())
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())

})

func CreateRedisCluster() *v1alpha1.RedisCluster {
	cluster := &v1alpha1.RedisCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RedisCluster",
			APIVersion: "redis.containersolutions.com/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rediscluster-sample",
			Namespace: "default",
		},
		Spec: v1alpha1.RedisClusterSpec{
			Auth: v1alpha1.RedisAuth{
				Enabled: false,
			},
			Version:  "5.0.5",
			Replicas: 1,
			RedisGraph: v1alpha1.RedisGraph{
				Enabled: true,
			},
		},
	}
	return cluster
}

/*
   	// Foo is an example field of RedisCluster. Edit rediscluster_types.go to remove/update
	Auth       RedisAuth  `json:"auth,omitempty"`
	Version    string     `json:"version,omitempty"`
	Replicas   int        `json:"replicas,omitempty"`
	RedisGraph RedisGraph `json:"redis-graph,omitempty"`
*/
