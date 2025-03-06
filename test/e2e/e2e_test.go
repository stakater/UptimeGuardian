/*
Copyright 2025.

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

package e2e

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stakater/UptimeGuardian/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stakater/UptimeGuardian/test/utils"
)

const (
	timeout  = time.Second * 30
	interval = time.Second * 1
)

// getRouteGVR returns the GroupVersionResource for OpenShift routes
func getRouteGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "route.openshift.io",
		Version:  "v1",
		Resource: "routes",
	}
}

// createTestRoutes creates test routes in the spoke cluster
func createTestRoutes(ctx context.Context, client dynamic.Interface, namespace, name string, labels map[string]string) error {
	route := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "route.openshift.io/v1",
			"kind":       "Route",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"labels":    labels,
			},
			"spec": map[string]interface{}{
				"host": fmt.Sprintf("%s.example.com", name),
				"to": map[string]interface{}{
					"kind": "Service",
					"name": "test-service",
				},
				"tls": map[string]interface{}{
					"termination": "edge",
				},
			},
		},
	}

	_, err := client.Resource(getRouteGVR()).Namespace(namespace).Create(ctx, route, metav1.CreateOptions{})
	return err
}

// setupSpokeClient creates a dynamic client for the spoke cluster
func setupSpokeClient(ctx context.Context, c client.Client, hostedCluster *hypershiftv1beta1.HostedCluster) (dynamic.Interface, error) {
	kubeconfigSecretName := fmt.Sprintf("%s-admin-kubeconfig", hostedCluster.Name)
	secret := &v1.Secret{}
	err := c.Get(ctx, types.NamespacedName{
		Name:      kubeconfigSecretName,
		Namespace: hostedCluster.Namespace,
	}, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig secret: %v", err)
	}

	kubeconfigData, exists := secret.Data["kubeconfig"]
	if !exists {
		return nil, fmt.Errorf("kubeconfig data not found in secret")
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("failed to create rest config: %v", err)
	}

	return dynamic.NewForConfigOrDie(restConfig), nil
}

// getProbeName returns the name for a Probe CR based on cluster name and route details
func getProbeName(clusterName, routeNamespace, routeName string) string {
	return fmt.Sprintf("%s-%s-%s", clusterName, routeNamespace, routeName)
}

var _ = Describe("UptimeGuardian E2E", Ordered, func() {
	var (
		k8sClient     client.Client
		spokeClient   dynamic.Interface
		ctx           context.Context
		hostedCluster *hypershiftv1beta1.HostedCluster
	)

	// Required environment variables
	var (
		hostedClusterName      = os.Getenv("HOSTED_CLUSTER_NAME")      // Name of the HostedCluster to test against
		hostedClusterNamespace = os.Getenv("HOSTED_CLUSTER_NAMESPACE") // Namespace where the HostedCluster exists
		hubNamespace           = os.Getenv("HUB_NAMESPACE")            // Namespace in hub cluster where UptimeProbe will be created
		spokeRouteNamespace    = os.Getenv("SPOKE_ROUTE_NAMESPACE")    // Namespace in spoke cluster where route exists
		routeName              = os.Getenv("ROUTE_NAME")               // Name of the route to monitor
		routeLabels            = os.Getenv("ROUTE_LABELS")             // Labels of the route in key=value format
		probeJobName           = os.Getenv("PROBE_JOB_NAME")           // Job name for the Probe CR
		probeInterval          = os.Getenv("PROBE_INTERVAL")           // Interval for the Probe CR
		probeScrapeTimeout     = os.Getenv("PROBE_TIMEOUT")            // Scrape timeout for the Probe CR
		probeModule            = os.Getenv("PROBE_MODULE")             // Module for the Probe CR
		proberUrl              = os.Getenv("PROBER_URL")               // URL of the blackbox exporter
		proberScheme           = os.Getenv("PROBER_SCHEME")            // Scheme for the prober
		proberPath             = os.Getenv("PROBER_PATH")              // Path for the prober
	)

	BeforeAll(func() {
		// Verify required environment variables
		requiredEnvs := map[string]string{
			"HOSTED_CLUSTER_NAME":      hostedClusterName,
			"HOSTED_CLUSTER_NAMESPACE": hostedClusterNamespace,
			"HUB_NAMESPACE":            hubNamespace,
			"SPOKE_ROUTE_NAMESPACE":    spokeRouteNamespace,
			"ROUTE_NAME":               routeName,
			"ROUTE_LABELS":             routeLabels,
			"PROBE_JOB_NAME":           probeJobName,
			"PROBE_INTERVAL":           probeInterval,
			"PROBE_TIMEOUT":            probeScrapeTimeout,
			"PROBE_MODULE":             probeModule,
			"PROBER_URL":               proberUrl,
			"PROBER_SCHEME":            proberScheme,
			"PROBER_PATH":              proberPath,
		}

		for env, value := range requiredEnvs {
			if value == "" {
				Fail(fmt.Sprintf("Environment variable %s is required", env))
			}
		}

		By("bootstrapping test environment")
		var err error
		k8sClient, err = utils.GetE2EClient()
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient).NotTo(BeNil())

		ctx = context.Background()

		// Verify required namespaces exist
		requiredNamespaces := []string{hubNamespace, hubNamespace}
		for _, ns := range requiredNamespaces {
			namespace := &v1.Namespace{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: ns}, namespace)
			if err != nil {
				// Create namespace if it doesn't exist
				namespace = &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: ns,
					},
				}
				Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
			}
		}

		// Get the HostedCluster
		By(fmt.Sprintf("Getting HostedCluster %s in namespace %s", hostedClusterName, hostedClusterNamespace))
		hostedCluster = &hypershiftv1beta1.HostedCluster{}
		err = k8sClient.Get(ctx, types.NamespacedName{
			Name:      hostedClusterName,
			Namespace: hostedClusterNamespace,
		}, hostedCluster)
		Expect(err).NotTo(HaveOccurred(), "HostedCluster should exist")

		// Setup spoke client using shared function
		By("Setting up spoke cluster client")
		spokeClient, err = setupSpokeClient(ctx, k8sClient, hostedCluster)
		Expect(err).NotTo(HaveOccurred(), "Should get spoke client")

		// Create test routes in spoke cluster using shared function
		By("Creating test routes in spoke cluster")
		err = createTestRoutes(ctx, spokeClient, spokeRouteNamespace, routeName, utils.ParseLabels(routeLabels))
		Expect(err).NotTo(HaveOccurred(), "Should create test routes")
	})

	AfterAll(func() {
		// Cleanup test routes
		By("Cleaning up test routes")
		if spokeClient != nil {
			err := spokeClient.Resource(getRouteGVR()).Namespace(spokeRouteNamespace).Delete(ctx, routeName, metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred(), "Should delete test routes")
		}
	})

	Context("When creating an UptimeProbe", func() {
		var uptimeProbe *v1alpha1.UptimeProbe
		var skipCleanup bool // Add flag to skip cleanup

		BeforeEach(func() {
			skipCleanup = false // Reset flag
			// Create UptimeProbe
			uptimeProbe = &v1alpha1.UptimeProbe{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "e2e-test-uptime-probe",
					Namespace: hubNamespace,
				},
				Spec: v1alpha1.UptimeProbeSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: utils.ParseLabels(routeLabels),
					},
					ProbeConfig: v1alpha1.ProbeConfig{
						JobName:         probeJobName,
						Interval:        monitoringv1.Duration(probeInterval),
						Module:          probeModule,
						ScrapeTimeout:   monitoringv1.Duration(probeScrapeTimeout),
						ProberUrl:       proberUrl,
						ProberScheme:    proberScheme,
						ProberPath:      proberPath,
						TargetNamespace: hubNamespace,
					},
				},
			}
		})

		AfterEach(func() {
			if skipCleanup {
				return
			}
			// Cleanup
			By("Cleaning up the UptimeProbe")
			err := k8sClient.Delete(ctx, uptimeProbe)
			Expect(err).NotTo(HaveOccurred())

			// Wait for probe to be deleted
			By("Cleaning up the monitoring Probes")
			Eventually(func() error {
				probe := &monitoringv1.Probe{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      getProbeName(hostedClusterName, spokeRouteNamespace, routeName),
					Namespace: hubNamespace,
				}, probe)
			}, timeout, interval).Should(HaveOccurred())
		})

		It("should handle label selector changes correctly", func() {
			// Initial labels for two routes
			route1Labels := utils.ParseLabels(routeLabels)                     // Use the same labels as the created route
			route2Labels := map[string]string{"app": "app2", "env": "staging"} // Different labels for testing

			// Create second route with different labels
			By("Creating second route with different labels")
			err := createTestRoutes(ctx, spokeClient, spokeRouteNamespace, "route2", route2Labels)
			Expect(err).NotTo(HaveOccurred(), "Should create second test route")

			By("Creating UptimeProbe matching route1")
			uptimeProbe.Spec.LabelSelector.MatchLabels = route1Labels
			Expect(k8sClient.Create(ctx, uptimeProbe)).Should(Succeed())

			By("Verifying Probe CR is created for route1")
			Eventually(func() error {
				probe := &monitoringv1.Probe{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      getProbeName(hostedClusterName, spokeRouteNamespace, routeName),
					Namespace: hubNamespace,
				}, probe)
			}, timeout, interval).Should(Succeed())

			By("Verifying no Probe CR exists for route2")
			Consistently(func() error {
				probe := &monitoringv1.Probe{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      getProbeName(hostedClusterName, spokeRouteNamespace, "route2"),
					Namespace: hubNamespace,
				}, probe)
			}, "10s", interval).Should(HaveOccurred())

			By("Updating UptimeProbe to match route2")
			uptimeProbe.Spec.LabelSelector.MatchLabels = route2Labels
			Expect(k8sClient.Update(ctx, uptimeProbe)).Should(Succeed())

			By("Verifying Probe CR for route1 is deleted")
			Eventually(func() error {
				probe := &monitoringv1.Probe{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      getProbeName(hostedClusterName, spokeRouteNamespace, routeName),
					Namespace: hubNamespace,
				}, probe)
			}, timeout, interval).Should(HaveOccurred())

			By("Verifying Probe CR for route2 is created")
			Eventually(func() error {
				probe := &monitoringv1.Probe{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      getProbeName(hostedClusterName, spokeRouteNamespace, "route2"),
					Namespace: hubNamespace,
				}, probe)
			}, timeout, interval).Should(Succeed())

			// Cleanup the second route
			By("Cleaning up second route")
			err = spokeClient.Resource(getRouteGVR()).Namespace(spokeRouteNamespace).Delete(ctx, "route2", metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred(), "Should delete second test route")
		})

		It("should create a Probe CR for matching route", func() {
			By("Creating UptimeProbe")
			Expect(k8sClient.Create(ctx, uptimeProbe)).Should(Succeed())

			By("Verifying Probe CR is created")
			Eventually(func() error {
				probe := &monitoringv1.Probe{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      getProbeName(hostedClusterName, spokeRouteNamespace, routeName),
					Namespace: hubNamespace,
				}, probe)
			}, timeout, interval).Should(Succeed())
		})

		It("should update Probe CR when UptimeProbe is updated", func() {
			By("Creating UptimeProbe")
			Expect(k8sClient.Create(ctx, uptimeProbe)).Should(Succeed())

			By("Updating UptimeProbe")
			updatedJobName := "updated-job"
			uptimeProbe.Spec.ProbeConfig.JobName = updatedJobName
			Expect(k8sClient.Update(ctx, uptimeProbe)).Should(Succeed())

			By("Verifying Probe CR is updated")
			Eventually(func() bool {
				probe := &monitoringv1.Probe{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      getProbeName(hostedClusterName, spokeRouteNamespace, routeName),
					Namespace: hubNamespace,
				}, probe)
				if err != nil {
					return false
				}
				return probe.Spec.JobName == updatedJobName
			}, timeout, interval).Should(BeTrue())
		})

		It("should delete Probe CR when UptimeProbe is deleted", func() {
			skipCleanup = true // Skip AfterEach cleanup for this test

			By("Creating UptimeProbe")
			Expect(k8sClient.Create(ctx, uptimeProbe)).Should(Succeed())

			By("Verifying Probe CR is created")
			Eventually(func() error {
				probe := &monitoringv1.Probe{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      getProbeName(hostedClusterName, spokeRouteNamespace, routeName),
					Namespace: hubNamespace,
				}, probe)
			}, timeout, interval).Should(Succeed())

			By("Deleting UptimeProbe")
			Expect(k8sClient.Delete(ctx, uptimeProbe)).Should(Succeed())

			By("Verifying Probe CR is deleted")
			Eventually(func() error {
				probe := &monitoringv1.Probe{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      getProbeName(hostedClusterName, spokeRouteNamespace, routeName),
					Namespace: hubNamespace,
				}, probe)
			}, timeout, interval).Should(HaveOccurred())
		})

		It("should handle route without matching labels", func() {
			By("Creating UptimeProbe with non-matching labels")
			uptimeProbe.Spec.LabelSelector.MatchLabels = map[string]string{
				"app": "different-app", // Different from routeLabels which has app=example
				"env": "different-env", // Different from routeLabels which has env=prod
			}
			Expect(k8sClient.Create(ctx, uptimeProbe)).Should(Succeed())

			By("Verifying no Probe CR is created")
			Consistently(func() error {
				probe := &monitoringv1.Probe{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      getProbeName(hostedClusterName, spokeRouteNamespace, routeName),
					Namespace: hubNamespace,
				}, probe)
			}, "10s", interval).Should(HaveOccurred())
		})
	})

	Context("When creating UptimeProbe for hub cluster routes", func() {
		var (
			uptimeProbe      *v1alpha1.UptimeProbe
			skipCleanup      bool
			hubDynamicClient dynamic.Interface
		)

		BeforeEach(func() {
			skipCleanup = false

			// Create dynamic client for hub cluster
			config, err := utils.GetKubeConfigFromEnv()
			Expect(err).NotTo(HaveOccurred(), "Should get kubeconfig")

			hubDynamicClient, err = dynamic.NewForConfig(config)
			Expect(err).NotTo(HaveOccurred(), "Should create dynamic client")

			// Create UptimeProbe for hub cluster
			uptimeProbe = &v1alpha1.UptimeProbe{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "e2e-test-hub-uptime-probe",
					Namespace: hubNamespace,
				},
				Spec: v1alpha1.UptimeProbeSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: utils.ParseLabels(routeLabels),
					},
					ProbeConfig: v1alpha1.ProbeConfig{
						JobName:         "hub-" + probeJobName,
						Interval:        monitoringv1.Duration(probeInterval),
						Module:          probeModule,
						ScrapeTimeout:   monitoringv1.Duration(probeScrapeTimeout),
						ProberUrl:       proberUrl,
						ProberScheme:    proberScheme,
						ProberPath:      proberPath,
						TargetNamespace: hubNamespace,
					},
				},
			}
		})

		AfterEach(func() {
			if skipCleanup {
				return
			}
			By("Cleaning up the UptimeProbe")
			err := k8sClient.Delete(ctx, uptimeProbe)
			Expect(err).NotTo(HaveOccurred())

			// Delete test routes using dynamic client
			routes := []string{"hub-route1", "hub-route2"}
			for _, route := range routes {
				_ = hubDynamicClient.Resource(getRouteGVR()).Namespace(hubNamespace).Delete(ctx, route, metav1.DeleteOptions{})
			}
		})

		It("should handle multiple hub cluster routes", func() {
			By("Creating two routes in hub cluster")
			route1Labels := utils.ParseLabels(routeLabels)
			route2Labels := map[string]string{"app": "hub-app2", "env": "staging"}

			// Create routes in hub cluster using dynamic client
			err := createTestRoutes(ctx, hubDynamicClient, hubNamespace, "hub-route1", route1Labels)
			Expect(err).NotTo(HaveOccurred())
			err = createTestRoutes(ctx, hubDynamicClient, hubNamespace, "hub-route2", route2Labels)
			Expect(err).NotTo(HaveOccurred())

			By("Creating UptimeProbe matching route1")
			uptimeProbe.Spec.LabelSelector.MatchLabels = route1Labels
			Expect(k8sClient.Create(ctx, uptimeProbe)).Should(Succeed())

			By("Verifying Probe CR is created for route1")
			Eventually(func() error {
				probe := &monitoringv1.Probe{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      fmt.Sprintf("%s-%s-%s", "host", hubNamespace, "hub-route1"),
					Namespace: hubNamespace,
				}, probe)
			}, timeout, interval).Should(Succeed())

			By("Verifying no Probe CR exists for route2")
			Consistently(func() error {
				probe := &monitoringv1.Probe{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      fmt.Sprintf("%s-%s-%s", "host", hubNamespace, "hub-route2"),
					Namespace: hubNamespace,
				}, probe)
			}, "10s", interval).Should(HaveOccurred())

			By("Updating UptimeProbe to match route2")
			uptimeProbe.Spec.LabelSelector.MatchLabels = route2Labels
			Expect(k8sClient.Update(ctx, uptimeProbe)).Should(Succeed())

			By("Verifying Probe CR for route1 is deleted")
			Eventually(func() error {
				probe := &monitoringv1.Probe{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      fmt.Sprintf("%s-%s-%s", "host", hubNamespace, "hub-route1"),
					Namespace: hubNamespace,
				}, probe)
			}, timeout, interval).Should(HaveOccurred())

			By("Verifying Probe CR for route2 is created")
			Eventually(func() error {
				probe := &monitoringv1.Probe{}
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      fmt.Sprintf("%s-%s-%s", "host", hubNamespace, "hub-route2"),
					Namespace: hubNamespace,
				}, probe)
			}, timeout, interval).Should(Succeed())
		})
	})
})
