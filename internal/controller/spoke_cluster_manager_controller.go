package controller

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	v12 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type ClusterManager struct {
	Manager manager.Manager
	Cancel  context.CancelFunc
}

type SpokeClusterManagerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Managers map[string]ClusterManager
	log      logr.Logger
}

//+kubebuilder:rbac:groups=networking.stakater.com,resources=uptimeprobes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.stakater.com,resources=uptimeprobes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=networking.stakater.com,resources=uptimeprobes/finalizers,verbs=update

func (r *SpokeClusterManagerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.log = log.FromContext(ctx)

	// Retrieve the HostedCluster (Spoke cluster)
	hostedCluster := &v1beta1.HostedCluster{}
	err := r.Get(context.TODO(), req.NamespacedName, hostedCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Handle the creation of a manager for the new HostedCluster
	if hostedCluster.DeletionTimestamp.IsZero() {
		// Create a manager for the Spoke cluster
		err := r.createManagerForSpokeCluster(ctx, hostedCluster)
		if err != nil {
			return reconcile.Result{}, err
		}
	} else {
		// Handle cleanup when HostedCluster is deleted
		err := r.cleanupManagerForSpokeCluster(hostedCluster)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *SpokeClusterManagerReconciler) createManagerForSpokeCluster(ctx context.Context, hostedCluster *v1beta1.
	HostedCluster) error {
	// Retrieve kubeconfig for the Spoke cluster
	kubeconfig, err := r.getKubeConfig(hostedCluster)
	if err != nil {
		r.log.Info(fmt.Sprintf("No kubeconfig found for hosted cluster %s", hostedCluster.Name))
		return nil
	}

	// Create a cancellable context to stop the manager later
	ctx, cancel := context.WithCancel(ctx)

	// Create the manager using the Spoke cluster's kubeconfig
	mgr, err := manager.New(kubeconfig, manager.Options{
		Scheme: r.Scheme,
		Metrics: server.Options{
			BindAddress: "0",
		},
	})
	if err != nil {
		cancel()
		return err
	}

	// Set the manager to watch the resources inside the Spoke cluster
	err = r.setupSpokeClusterWatch(mgr, hostedCluster)
	if err != nil {
		cancel()
		return err
	}

	// Store the manager and its cancel function in the registry
	if r.Managers == nil {
		r.Managers = make(map[string]ClusterManager)
	}

	r.Managers[hostedCluster.Name] = ClusterManager{
		Manager: mgr,
		Cancel:  cancel,
	}

	r.log.Info(fmt.Sprintf("added manager for hosted cluster %s", hostedCluster.Name))
	// Start the manager in a separate Go routine
	go func() {
		if err := mgr.Start(ctx); err != nil {
			r.log.Error(err, "failed to start manager for Spoke cluster", "cluster", hostedCluster.Name)
		}
	}()

	return nil
}

func (r *SpokeClusterManagerReconciler) cleanupManagerForSpokeCluster(hostedCluster *v1beta1.HostedCluster) error {
	if clusterMgr, exists := r.Managers[hostedCluster.Name]; exists {
		clusterMgr.Cancel()
		r.log.Info(fmt.Sprintf("removed manager for hosted cluster %s", hostedCluster.Name))
		delete(r.Managers, hostedCluster.Name)
	}

	return nil
}

func (r *SpokeClusterManagerReconciler) getKubeConfig(hostedCluster *v1beta1.HostedCluster) (*rest.
	Config,
	error) {
	kubeconfigSecretName := fmt.Sprintf("%s-admin-kubeconfig", hostedCluster.Name)

	// Retrieve the secret containing the kubeconfig
	secret := &v12.Secret{}
	err := r.Get(context.TODO(), client.ObjectKey{
		Name:      kubeconfigSecretName,
		Namespace: hostedCluster.Namespace,
	}, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve kubeconfig secret for HostedCluster %s: %v", hostedCluster.Name, err)
	}

	// The kubeconfig is stored under the key "kubeconfig" in the secret data
	kubeconfigData, exists := secret.Data["kubeconfig"]
	if !exists {
		return nil, fmt.Errorf("kubeconfig data not found in secret %s", kubeconfigSecretName)
	}

	// Decode the kubeconfig (it's typically base64 encoded in the secret)
	kubeconfig := string(kubeconfigData)
	restConfig, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("failed to create rest.Config from kubeconfig for HostedCluster %s: %v", hostedCluster.Name, err)
	}

	return restConfig, nil
}

// Set up the watches for the Spoke cluster using the manager
func (r *SpokeClusterManagerReconciler) setupSpokeClusterWatch(mgr manager.Manager, hostedCluster *v1beta1.HostedCluster) error {
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SpokeClusterManagerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.HostedCluster{}).
		Complete(r)
}
