package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	v12 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type SpokeManager struct {
	dynamic.Interface
	stopInformerChan chan struct{}
}

type SpokeClusterManagerReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	RemoteClients map[string]SpokeManager
	log           logr.Logger
	manager       ctrl.Manager
}

//+kubebuilder:rbac:groups=networking.stakater.com,resources=uptimeprobes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.stakater.com,resources=uptimeprobes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=networking.stakater.com,resources=uptimeprobes/finalizers,verbs=update
//+kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

func (r *SpokeClusterManagerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.log = log.FromContext(ctx)

	// Retrieve the HostedCluster (Spoke cluster)
	hostedCluster := &v1beta1.HostedCluster{}
	err := r.Get(ctx, req.NamespacedName, hostedCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Handle the creation of a manager for the new HostedCluster
	if hostedCluster.DeletionTimestamp.IsZero() {
		// Create a manager for the Spoke cluster
		err := r.setupRemoteClientForSpokeCluster(hostedCluster)
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

func (r *SpokeClusterManagerReconciler) setupRemoteClientForSpokeCluster(hostedCluster *v1beta1.
	HostedCluster) error {
	kubeconfig, err := r.getKubeConfig(hostedCluster)
	if err != nil {
		r.log.Info(fmt.Sprintf("No kubeconfig found for hosted cluster %s", hostedCluster.Name))
		err = r.cleanupManagerForSpokeCluster(hostedCluster)
		if err != nil {
			return err
		}
		return nil
	}

	if r.RemoteClients == nil {
		r.RemoteClients = make(map[string]SpokeManager)
	}

	if _, ok := r.RemoteClients[hostedCluster.Name]; ok {
		return nil
	}

	r.RemoteClients[hostedCluster.Name] = SpokeManager{
		Interface:        dynamic.NewForConfigOrDie(kubeconfig),
		stopInformerChan: make(chan struct{}),
	}

	return (&SpokeRouteReconciler{
		Client:       r.Client,
		RemoteClient: r.RemoteClients[hostedCluster.Name].Interface,
		Scheme:       r.Scheme,
		Name:         hostedCluster.Name,
		Stop:         r.RemoteClients[hostedCluster.Name].stopInformerChan,
	}).SetupWithManager(r.manager)
}

func (r *SpokeClusterManagerReconciler) cleanupManagerForSpokeCluster(hostedCluster *v1beta1.HostedCluster) error {
	if sp, exists := r.RemoteClients[hostedCluster.Name]; exists {
		r.log.Info(fmt.Sprintf("removed remote client for hosted cluster %s", hostedCluster.Name))
		close(sp.stopInformerChan)
		delete(r.RemoteClients, hostedCluster.Name)
	}

	return nil
}

func (r *SpokeClusterManagerReconciler) getKubeConfig(hostedCluster *v1beta1.HostedCluster) (*rest.Config, error) {
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

	return getRestConfig(kubeconfigData)
}

var getRestConfig = func(kubeconfigData []byte) (*rest.Config, error) {
	return clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
}

// SetupWithManager sets up the controller with the Manager.
func (r *SpokeClusterManagerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.manager = mgr
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.HostedCluster{}).
		Complete(r)
}
