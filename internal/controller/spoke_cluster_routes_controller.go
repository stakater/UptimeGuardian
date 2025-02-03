package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	networkingv1alpha1 "github.com/stakater/UptimeGuardian/api/v1alpha1"
	v12 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	LabelClusterName    = "stakater.com/cluster-name"
	LabelRouteName      = "stakater.com/route-name"
	LabelRouteNamespace = "stakater.com/route-namespace"
)

type SpokeClusterRoutesReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	log          logr.Logger
	UptimeConfig *networkingv1alpha1.UptimeProbe
}

//+kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list

func (r *SpokeClusterRoutesReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.log = log.FromContext(ctx)
	r.log.Info("Reconciling HostedCluster", "namespace", req.Namespace, "name", req.Name)

	// Fetch the HostedCluster
	cluster := &v1beta1.HostedCluster{}
	if err := r.Get(context.TODO(), req.NamespacedName, cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// cleanup if the cluster is being deleted
	if !cluster.DeletionTimestamp.IsZero() {
		if err := r.cleanupClusterProbes(ctx, cluster); err != nil {
			r.log.Error(err, "Failed to cleanup cluster probes")
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
		return ctrl.Result{}, nil
	}

	if err := r.processHostedCluster(ctx, cluster); err != nil {
		r.log.Error(err, "Failed to process cluster", "cluster", cluster.Name)
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}

	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

func (r *SpokeClusterRoutesReconciler) processHostedCluster(ctx context.Context, cluster *v1beta1.HostedCluster) error {
	if r.UptimeConfig == nil {
		r.log.Info("No UptimeProbe configuration found, skipping reconciliation")
		return nil
	}

	// Get kubeconfig for the hosted cluster
	config, err := r.getKubeConfig(cluster)
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	// Create client for the hosted cluster
	spokeClient, err := r.createSpokeClient(config)
	if err != nil {
		return fmt.Errorf("failed to create spoke client: %w", err)
	}

	// Collect routes from the hosted cluster with label selector if specified
	routes, err := r.collectRoutes(ctx, spokeClient)
	if err != nil {
		return fmt.Errorf("failed to collect routes: %w", err)
	}

	// Process each route
	for _, route := range routes.Items {
		r.log.Info("Processing route", "cluster", cluster.Name, "route", route.Name, "namespace", route.Namespace)
		if err := r.processRoute(ctx, cluster, &route); err != nil {
			r.log.Error(err, "Failed to process route",
				"cluster", cluster.Name,
				"route", route.Name,
				"namespace", route.Namespace)
		}
	}

	return nil
}

func (r *SpokeClusterRoutesReconciler) createSpokeClient(config *rest.Config) (client.Client, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(routev1.AddToScheme(scheme))
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	return client.New(config, client.Options{
		Scheme: scheme,
	})
}

func (r *SpokeClusterRoutesReconciler) collectRoutes(ctx context.Context, spokeClient client.Client) (*routev1.RouteList, error) {
	routes := &routev1.RouteList{}
	listOpts := []client.ListOption{}

	// Add label selector if specified
	if r.UptimeConfig.Spec.LabelSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(r.UptimeConfig.Spec.LabelSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to parse label selector: %w", err)
		}
		listOpts = append(listOpts, client.MatchingLabelsSelector{Selector: selector})
	}

	if err := spokeClient.List(ctx, routes, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list routes: %w", err)
	}
	return routes, nil
}

func (r *SpokeClusterRoutesReconciler) processRoute(ctx context.Context, cluster *v1beta1.HostedCluster, route *routev1.Route) error {
	// First try to find existing probe by labels
	probes := &monitoringv1.ProbeList{}
	if err := r.List(ctx, probes, client.MatchingLabels{
		LabelClusterName:    cluster.Name,
		LabelRouteName:      route.Name,
		LabelRouteNamespace: route.Namespace,
	}); err != nil {
		return fmt.Errorf("failed to list probes: %w", err)
	}

	// Map route to probe configuration
	probe := r.mapRouteToProbe(cluster, route)

	// Handle existing or create new
	if len(probes.Items) > 0 {
		existing := probes.Items[0]
		existing.Annotations = probe.Annotations
		// Update spec
		existing.Spec = probe.Spec
		existing.Labels = probe.Labels
		if err := r.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to update probe: %w", err)
		}
		r.log.Info("Updated existing probe", "name", existing.Name)
		return nil
	}

	// Create new probe
	if err := r.Create(ctx, probe); err != nil {
		return fmt.Errorf("failed to create probe: %w", err)
	}
	r.log.Info("Created new probe", "name", probe.Name)
	return nil
}

func (r *SpokeClusterRoutesReconciler) mapRouteToProbe(cluster *v1beta1.HostedCluster, route *routev1.Route) *monitoringv1.Probe {
	probeName := fmt.Sprintf("%s-%s-%s", cluster.Name, route.Namespace, route.Name)

	// Build target URL
	targetURL := route.Spec.Host
	if route.Spec.TLS != nil {
		targetURL = fmt.Sprintf("https://%s", targetURL)
	} else {
		targetURL = fmt.Sprintf("http://%s", targetURL)
	}

	return &monitoringv1.Probe{
		ObjectMeta: metav1.ObjectMeta{
			Name:      probeName,
			Namespace: r.UptimeConfig.Spec.TargetNamespace,
			Labels: map[string]string{
				LabelClusterName:    cluster.Name,
				LabelRouteName:      route.Name,
				LabelRouteNamespace: route.Namespace,
			},
			Annotations: route.Annotations,
		},
		Spec: monitoringv1.ProbeSpec{
			JobName:  r.UptimeConfig.Spec.ProberConfig.JobName,
			Interval: r.UptimeConfig.Spec.ProberConfig.Interval,
			Module:   r.UptimeConfig.Spec.ProberConfig.Module,
			ProberSpec: monitoringv1.ProberSpec{
				URL:    r.UptimeConfig.Spec.ProberConfig.URL,
				Scheme: r.UptimeConfig.Spec.ProberConfig.Scheme,
				Path:   r.UptimeConfig.Spec.ProberConfig.Path,
			},
			Targets: monitoringv1.ProbeTargets{
				StaticConfig: &monitoringv1.ProbeTargetStaticConfig{
					Targets: []string{targetURL},
				},
			},
		},
	}
}

func (r *SpokeClusterRoutesReconciler) cleanupClusterProbes(ctx context.Context, cluster *v1beta1.HostedCluster) error {
	// List all probes for this cluster
	probes := &monitoringv1.ProbeList{}
	if err := r.List(ctx, probes, client.MatchingLabels{
		LabelClusterName: cluster.Name,
	}); err != nil {
		return fmt.Errorf("failed to list probes: %w", err)
	}

	// Delete each probe
	for _, probe := range probes.Items {
		if err := r.Delete(ctx, probe); err != nil {
			return fmt.Errorf("failed to delete probe %s: %w", probe.Name, err)
		}
		r.log.Info("Deleted probe", "name", probe.Name)
	}

	return nil
}

func (r *SpokeClusterRoutesReconciler) getKubeConfig(hostedCluster *v1beta1.HostedCluster) (*rest.Config, error) {
	kubeconfigSecretName := fmt.Sprintf("%s-admin-kubeconfig", hostedCluster.Name)

	secret := &v12.Secret{}
	err := r.Get(context.TODO(), client.ObjectKey{
		Name:      kubeconfigSecretName,
		Namespace: hostedCluster.Namespace,
	}, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig secret: %w", err)
	}

	kubeconfigData, exists := secret.Data["kubeconfig"]
	if !exists {
		return nil, fmt.Errorf("kubeconfig key not found in secret")
	}

	return clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
}

func (r *SpokeClusterRoutesReconciler) cleanupAllResources(ctx context.Context) error {
	r.log.Info("Starting cleanup of all resources")

	// TODO: which namespace should hold all these Probe resources?
	// List all probes in the monitoring namespace
	probes := &monitoringv1.ProbeList{}
	if err := r.List(ctx, probes, client.InNamespace(r.UptimeConfig.Spec.TargetNamespace)); err != nil {
		return fmt.Errorf("failed to list probes during cleanup: %w", err)
	}

	// Delete all probes
	for i := range probes.Items {
		if err := r.Delete(ctx, probes.Items[i]); err != nil {
			r.log.Error(err, "Failed to delete probe during cleanup",
				"name", probes.Items[i].Name)
		} else {
			r.log.Info("Deleted probe during cleanup",
				"name", probes.Items[i].Name)
		}
	}

	r.log.Info("Completed cleanup of all resources")
	return nil
}

// Add cleanup setup method
func (r *SpokeClusterRoutesReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create cleanup function
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		if err := r.cleanupAllResources(ctx); err != nil {
			r.log.Error(err, "Failed to cleanup resources")
		}
	}

	// Register cleanup function
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		<-ctx.Done()
		cleanup()
		return nil
	})); err != nil {
		return fmt.Errorf("failed to register cleanup handler: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.HostedCluster{}).
		Complete(r)
}
