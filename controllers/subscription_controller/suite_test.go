/*


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

	operatorscoreoscomv1 "github.com/buptGophers/operator-manager/api/v1"
	operatorscoreoscomv1alpha1 "github.com/buptGophers/operator-manager/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	//corev1 "k8s.io/api/core/v1"
	"path/filepath"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var testEnv *envtest.Environment
var k8sManager manager.Manager
var k8sClient client.Client

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}

	var err error
	cfg, err := testEnv.Start()

	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	err = operatorscoreoscomv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = operatorscoreoscomv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = operatorscoreoscomv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).ToNot(HaveOccurred())
	Expect(k8sClient).ToNot(BeNil())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	err = (&SubscriptionReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("Subscription controller"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())
	go func() {
		err = k8sManager.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred())
	}()

	k8sClient = k8sManager.GetClient()

	Expect(k8sClient).ToNot(BeNil())

	close(done)
}, 60)

var _ = Describe("Subscription controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		//SubscriptionControllerName = "test-subscription-controller"
		SubscriptionControllerNamespace = "default"
		JobName                         = "test-job"

		timeout  = time.Second * 10
		duration = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When SubscriptionStatus is operated", func() {
		It("Should do nothing", func() {
			By("By creating a wanted but have been operated operator's Blueprint")
			ctx := context.Background()
			SubscriptionTestCase1 := operatorscoreoscomv1.Subscription{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "batch.tutorial.kubebuilder.io/v1",
					Kind:       "Subscription",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "SubscriptionTestCase1",
					Namespace: SubscriptionControllerNamespace,
				},
				Spec: operatorscoreoscomv1.SubscriptionSpec{
					StartingCSV: "testOperatorCSV1",
					Option:      "Create",
				},
				Status: operatorscoreoscomv1.SubscriptionStatus{
					OpStatus: "operated",
				},
			}
			Expect(k8sClient.Create(ctx, &SubscriptionTestCase1)).Should(Succeed())

			SubscriptionTestCase1LookupKey := types.NamespacedName{Name: SubscriptionTestCase1.Name, Namespace: SubscriptionControllerNamespace}
			testReq := ctrl.Request{NamespacedName: SubscriptionTestCase1LookupKey}

			_, err := (&SubscriptionReconciler{
				Client: k8sManager.GetClient(),
				Scheme: k8sManager.GetScheme(),
				Log:    ctrl.Log.WithName("controllers").WithName("Subscription controller"),
			}).Reconcile(testReq)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})
