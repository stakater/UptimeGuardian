package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stakater/UptimeGuardian/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("SpokeRouteController", func() {
	var (
		ctx         context.Context
		reconciler  *SpokeRouteReconciler
		uptimeProbe *v1alpha1.UptimeProbe
		route       *unstructured.Unstructured
		probe       *monitoringv1.Probe
		fakeClient  client.Client
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme := runtime.NewScheme()
		_ = monitoringv1.AddToScheme(scheme)
		_ = v1alpha1.AddToScheme(scheme)

		// Create test UptimeProbe
		uptimeProbe = &v1alpha1.UptimeProbe{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-probe",
				Namespace: "default",
			},
			Spec: v1alpha1.UptimeProbeSpec{
				LabelSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app": "test",
					},
				},
				ProbeConfig: v1alpha1.ProbeConfig{
					JobName:         "test-job",
					Interval:        "30s",
					Module:          "http_2xx",
					ScrapeTimeout:   "10s",
					ProberUrl:       "blackbox-exporter:9115",
					ProberScheme:    "http",
					ProberPath:      "/probe",
					TargetNamespace: "monitoring",
				},
			},
		}

		// Create test Route
		route = &unstructured.Unstructured{}
		route.SetUnstructuredContent(map[string]interface{}{
			"apiVersion": "route.openshift.io/v1",
			"kind":       "Route",
			"metadata": map[string]interface{}{
				"name":      "test-route",
				"namespace": "default",
				"labels": map[string]interface{}{
					"app": "test",
				},
			},
			"spec": map[string]interface{}{
				"host": "test.example.com",
				"tls":  map[string]interface{}{},
			},
		})

		// Create test Probe
		probe = &monitoringv1.Probe{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-default-test-route",
				Namespace: "monitoring",
				Labels: map[string]string{
					UptimeProbeLabel: "default-test-probe",
					ClusterNameLabel: "test-cluster",
				},
			},
		}

		// Setup fake clients
		fakeClient = fakeclient.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(uptimeProbe, probe).
			Build()

		var fakeDynamicClient dynamic.Interface = fake.NewSimpleDynamicClient(scheme, route)

		reconciler = &SpokeRouteReconciler{
			Client:       fakeClient,
			Scheme:       scheme,
			Name:         "test-cluster",
			RemoteClient: fakeDynamicClient,
			Stop:         make(chan struct{}),
		}
	})

	AfterEach(func() {
		close(reconciler.Stop)
	})

	Context("Reconciling Routes", func() {
		It("should create probes for matching routes", func() {
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      uptimeProbe.Name,
					Namespace: uptimeProbe.Namespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify probe was created
			createdProbe := &monitoringv1.Probe{}
			err = reconciler.Get(ctx, types.NamespacedName{
				Name:      "test-cluster-default-test-route",
				Namespace: "monitoring",
			}, createdProbe)
			Expect(err).NotTo(HaveOccurred())
			Expect(createdProbe.Spec.JobName).To(Equal(uptimeProbe.Spec.ProbeConfig.JobName))
		})

		It("should cleanup stale probes", func() {
			// Create a probe that doesn't match any routes
			staleProbe := &monitoringv1.Probe{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-default-stale-route",
					Namespace: "monitoring",
					Labels: map[string]string{
						UptimeProbeLabel: fmt.Sprintf("%v-%v", uptimeProbe.Namespace, uptimeProbe.Name),
						ClusterNameLabel: "test-cluster",
					},
				},
			}
			err := reconciler.Create(ctx, staleProbe)
			Expect(err).NotTo(HaveOccurred())

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      uptimeProbe.Name,
					Namespace: uptimeProbe.Namespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify stale probe was deleted
			err = reconciler.Get(ctx, types.NamespacedName{
				Name:      staleProbe.Name,
				Namespace: staleProbe.Namespace,
			}, &monitoringv1.Probe{})
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
		})

		It("should update existing probes", func() {
			// First create the probe
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      uptimeProbe.Name,
					Namespace: uptimeProbe.Namespace,
				},
			}

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Modify the UptimeProbe
			uptimeProbe.Spec.ProbeConfig.JobName = "updated-job"
			err = reconciler.Update(ctx, uptimeProbe)
			Expect(err).NotTo(HaveOccurred())

			// Reconcile again
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify probe was updated
			updatedProbe := &monitoringv1.Probe{}
			err = reconciler.Get(ctx, types.NamespacedName{
				Name:      "test-cluster-default-test-route",
				Namespace: "monitoring",
			}, updatedProbe)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedProbe.Spec.JobName).To(Equal("updated-job"))
		})

		It("should handle routes without hosts gracefully", func() {
			// Create a route without host
			invalidRoute := &unstructured.Unstructured{}
			invalidRoute.SetUnstructuredContent(map[string]interface{}{
				"apiVersion": "route.openshift.io/v1",
				"kind":       "Route",
				"metadata": map[string]interface{}{
					"name":      "invalid-route",
					"namespace": "default",
					"labels": map[string]interface{}{
						"app": "test",
					},
				},
				"spec": map[string]interface{}{},
			})

			// Replace the dynamic client with one containing the invalid route
			var newFakeDynamicClient dynamic.Interface = fake.NewSimpleDynamicClient(reconciler.Scheme, invalidRoute)
			reconciler.RemoteClient = newFakeDynamicClient

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      uptimeProbe.Name,
					Namespace: uptimeProbe.Namespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify no probe was created for the invalid route
			err = reconciler.Get(ctx, types.NamespacedName{
				Name:      "test-cluster-default-invalid-route",
				Namespace: "monitoring",
			}, &monitoringv1.Probe{})
			Expect(err).To(HaveOccurred())
		})
	})
})
