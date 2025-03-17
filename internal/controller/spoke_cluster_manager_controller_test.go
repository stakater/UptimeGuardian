package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	v12 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// mockClient wraps the fake client to handle deletion timestamps
type mockClient struct {
	client.Client
	deletedObjects map[string]bool
}

func (m *mockClient) MarkForDeletion(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	key := obj.GetNamespace() + "/" + obj.GetName()
	m.deletedObjects[key] = true
	return nil
}

func (m *mockClient) Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
	if err := m.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}

	// If the object is marked as deleted, set its deletion timestamp
	if m.deletedObjects[key.Namespace+"/"+key.Name] {
		now := metav1.Now()
		obj.SetDeletionTimestamp(&now)
	}
	return nil
}

func newMockClient(c client.Client) *mockClient {
	return &mockClient{
		Client:         c,
		deletedObjects: make(map[string]bool),
	}
}

func newMockSpokeManager() SpokeManager {
	return SpokeManager{
		Interface:        dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		stopInformerChan: make(chan struct{}),
	}
}

var _ = Describe("SpokeClusterManagerController", func() {
	var (
		ctx           context.Context
		reconciler    *SpokeClusterManagerReconciler
		hostedCluster *v1beta1.HostedCluster
		secret        *v12.Secret
		mockClient    *mockClient
	)

	BeforeEach(func() {
		// Set up mock function
		getRestConfig = func(kubeconfigData []byte) (*rest.Config, error) {
			return &rest.Config{
				Host: "https://test-cluster-api.example.com:6443",
				TLSClientConfig: rest.TLSClientConfig{
					Insecure: true,
				},
			}, nil
		}

		ctx = context.Background()

		// Create a test HostedCluster
		hostedCluster = &v1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// required for the reconciler to setup the remote client
		hostedCluster.DeletionTimestamp = nil

		// Create a test kubeconfig secret
		secret = &v12.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-admin-kubeconfig",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"kubeconfig": []byte("test-kubeconfig"), // Simple test data since we're mocking the client creation
			},
		}

		// Create fake client and wrap it with our mock
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(hostedCluster, secret).
			Build()
		mockClient = newMockClient(fakeClient)

		reconciler = &SpokeClusterManagerReconciler{
			Client:        mockClient,
			Scheme:        scheme.Scheme,
			RemoteClients: make(map[string]SpokeManager),
		}

		// Mock the dynamic client creation
		reconciler.RemoteClients[hostedCluster.Name] = newMockSpokeManager()
	})

	Context("Reconciling a HostedCluster", func() {
		It("should setup remote client for a new cluster", func() {
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      hostedCluster.Name,
					Namespace: hostedCluster.Namespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify remote client was created
			_, exists := reconciler.RemoteClients[hostedCluster.Name]
			Expect(exists).To(BeTrue())
		})

		It("should cleanup remote client when cluster is deleted", func() {
			// First create the remote client by reconciling the HostedCluster
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      hostedCluster.Name,
					Namespace: hostedCluster.Namespace,
				},
			}

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Delete the cluster using our mock client
			err = mockClient.MarkForDeletion(ctx, hostedCluster)
			Expect(err).NotTo(HaveOccurred())

			// Reconcile again
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify remote client was removed
			_, exists := reconciler.RemoteClients[hostedCluster.Name]
			Expect(exists).To(BeFalse())
		})

		It("should handle missing kubeconfig secret gracefully", func() {
			// Delete the secret
			err := mockClient.Delete(ctx, secret)
			Expect(err).NotTo(HaveOccurred())

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      hostedCluster.Name,
					Namespace: hostedCluster.Namespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify no remote client was created
			_, exists := reconciler.RemoteClients[hostedCluster.Name]
			Expect(exists).To(BeFalse())
		})

		It("should handle invalid kubeconfig data gracefully", func() {
			// Set mock to return an error
			getRestConfig = func(kubeconfigData []byte) (*rest.Config, error) {
				return nil, fmt.Errorf("invalid kubeconfig data %s", string(kubeconfigData))
			}

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      hostedCluster.Name,
					Namespace: hostedCluster.Namespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			// Verify no remote client was created
			_, exists := reconciler.RemoteClients[hostedCluster.Name]
			Expect(exists).To(BeFalse())
		})

		It("should not create duplicate remote clients", func() {
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      hostedCluster.Name,
					Namespace: hostedCluster.Namespace,
				},
			}

			// First reconciliation
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Get the first remote client
			firstClient, exists := reconciler.RemoteClients[hostedCluster.Name]
			Expect(exists).To(BeTrue())

			// Second reconciliation
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify the remote client wasn't replaced
			secondClient, exists := reconciler.RemoteClients[hostedCluster.Name]
			Expect(exists).To(BeTrue())
			Expect(secondClient).To(Equal(firstClient))
		})
	})

	Context("setupRemoteClientForHostCluster", func() {
		It("should not create duplicate host client", func() {
			// Setup initial state with existing host client
			reconciler.RemoteClients = make(map[string]SpokeManager)
			mockSpokeManager := newMockSpokeManager()
			reconciler.RemoteClients[clientKey] = mockSpokeManager

			// Try to setup host client again
			_, err := reconciler.setupRemoteClientForHostCluster()
			Expect(err).NotTo(HaveOccurred())

			// Verify the original client was not replaced
			hostClient := reconciler.RemoteClients[clientKey]
			Expect(hostClient).To(Equal(mockSpokeManager))
		})
	})
})
