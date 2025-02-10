package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stakater/UptimeGuardian/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// SpokeRouteReconciler reconciles a SpokeRoute object
type SpokeRouteReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Name         string
	RemoteClient dynamic.Interface
	Stop         chan struct{}
	logger       logr.Logger
}

const (
	UptimeProbeLabel = "uptime-probe"
	ClusterNameLabel = "cluster-name"
)

func (r *SpokeRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.FromContext(ctx).WithName(r.Name)
	r.logger.Info("Reconciling SpokeRoute")

	uptimeProbe := &v1alpha1.UptimeProbe{}
	if err := r.Get(ctx, req.NamespacedName, uptimeProbe); err != nil {
		if errors.IsNotFound(err) {
			// UptimeProbe is deleted, clean up any probes that were created by this UptimeProbe
			err = r.cleanupStaleProbes(ctx, req.Name, req.Namespace, nil)
			if err != nil {
				r.logger.Error(err, "Failed to cleanup probes for deleted UptimeProbe")
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	routes, err := r.getMatchingRoutes(ctx, uptimeProbe)
	if err != nil {
		return ctrl.Result{}, err
	}

	// delete earlier created probes which do not match the label selector anymore
	err = r.cleanupStaleProbes(ctx, uptimeProbe.Name, uptimeProbe.Namespace, routes)
	if err != nil {
		// continue with the rest of the reconciliation
		// TODO: should continue with error?
		r.logger.Error(err, fmt.Sprintf("Failed to cleanup stale probes for uptimeProbe %v", uptimeProbe.GetName()))
	}

	// create or update probes for each route
	for _, route := range routes.Items {
		if err := r.createOrUpdateProbe(ctx, uptimeProbe, &route); err != nil {
			r.logger.Error(err, fmt.Sprintf("Failed to process route %v", route.GetName()))
		}
	}

	return ctrl.Result{}, nil
}

func (r *SpokeRouteReconciler) cleanupStaleProbes(ctx context.Context, uptimeProbeName, uptimeProbeNamespace string, routes *unstructured.UnstructuredList) error {
	// Get all probes that were created by this uptimeProbe (using the uptimeProbe label)
	probes, err := r.getProbesMatchingUptimeProbeLabel(ctx, uptimeProbeName, uptimeProbeNamespace)
	if err != nil {
		return err
	}

	if len(probes.Items) == 0 {
		return nil
	}

	// If routes is nil, delete all probes (UptimeProbe was deleted)
	if routes == nil {
		for _, probe := range probes.Items {
			r.logger.Info(fmt.Sprintf("Deleting probe %v as UptimeProbe was deleted", probe.GetName()))
			if err := r.Delete(ctx, &probe); err != nil {
				r.logger.Error(err, fmt.Sprintf("Failed to delete probe %v", probe.GetName()))
			}
		}
		return nil
	}

	// Create a map of expected probe names based on current routes
	expectedProbes := make(map[string]bool)
	for _, route := range routes.Items {
		probeName := r.getProbeName(&route)
		expectedProbes[probeName] = true
	}

	// Delete any probe that was created by this uptimeProbe but is no longer needed
	for _, probe := range probes.Items {
		if !expectedProbes[probe.GetName()] {
			r.logger.Info(fmt.Sprintf("Deleting stale probe %v as its route no longer matches the label selector", probe.GetName()))
			if err := r.Delete(ctx, &probe); err != nil {
				r.logger.Error(err, fmt.Sprintf("Failed to delete probe %v", probe.GetName()))
			}
		}
	}

	return nil
}

func (r *SpokeRouteReconciler) getProbesMatchingUptimeProbeLabel(ctx context.Context, uptimeProbeName, uptimeProbeNamespace string) (*monitoringv1.ProbeList, error) {
	uptimeProbeLabels := r.getUptimeProbeLabels(uptimeProbeName, uptimeProbeNamespace)

	probes := &monitoringv1.ProbeList{}
	if err := r.List(ctx, probes, client.MatchingLabels(uptimeProbeLabels)); err != nil {
		return nil, err
	}

	return probes, nil
}

func (r *SpokeRouteReconciler) getMatchingRoutes(ctx context.Context, uptimeProbe *v1alpha1.UptimeProbe) (*unstructured.UnstructuredList, error) {
	routeGVR := schema.GroupVersionResource{
		Group:    "route.openshift.io",
		Version:  "v1",
		Resource: "routes",
	}
	return r.RemoteClient.Resource(routeGVR).Namespace("").List(ctx, v1.ListOptions{
		LabelSelector: labels.SelectorFromSet(uptimeProbe.Spec.LabelSelector.MatchLabels).String(),
	})
}

func (r *SpokeRouteReconciler) getTargetUrls(route *unstructured.Unstructured) ([]string, error) {
	host, _, _ := unstructured.NestedString(route.Object, "spec", "host")
	if host == "" {
		return nil, fmt.Errorf("route %v has no host", route.GetName())
	}

	path, _, _ := unstructured.NestedString(route.Object, "spec", "path")
	var targetUrls []string

	if tls, _, _ := unstructured.NestedMap(route.Object, "spec", "tls"); tls != nil {
		targetUrls = append(targetUrls, fmt.Sprintf("https://%v%v", host, path))
	}

	return targetUrls, nil
}

func (r *SpokeRouteReconciler) getDurationFromAnnotation(route *unstructured.Unstructured, annotationKey string, defaultValue monitoringv1.Duration) monitoringv1.Duration {
	if value, ok := route.GetAnnotations()[annotationKey]; ok {
		if _, err := time.ParseDuration(value); err != nil {
			r.logger.Error(err, fmt.Sprintf("Invalid %s annotation value %v for route %v, using default value %v",
				annotationKey, value, route.GetName(), defaultValue))
			return defaultValue
		}
		return monitoringv1.Duration(value)
	}
	return defaultValue
}

func (r *SpokeRouteReconciler) getUptimeProbeLabels(uptimeProbeName, uptimeProbeNamespace string) map[string]string {
	return map[string]string{
		UptimeProbeLabel: fmt.Sprintf("%v-%v", uptimeProbeNamespace, uptimeProbeName),
		ClusterNameLabel: r.Name,
	}
}

func (r *SpokeRouteReconciler) createProbe(ctx context.Context, uptimeProbe *v1alpha1.UptimeProbe, route *unstructured.Unstructured, probeName string, targetUrls []string) error {
	interval := r.getDurationFromAnnotation(route, "interval", uptimeProbe.Spec.ProbeConfig.Interval)
	scrapeTimeout := r.getDurationFromAnnotation(route, "scrapeTimeout", uptimeProbe.Spec.ProbeConfig.ScrapeTimeout)

	probe := &monitoringv1.Probe{
		ObjectMeta: v1.ObjectMeta{
			Name:      probeName,
			Namespace: uptimeProbe.Spec.ProbeConfig.TargetNamespace,
			Labels:    r.getUptimeProbeLabels(uptimeProbe.Name, uptimeProbe.Namespace),
		},
		Spec: monitoringv1.ProbeSpec{
			JobName:       uptimeProbe.Spec.ProbeConfig.JobName,
			Interval:      interval,
			Module:        uptimeProbe.Spec.ProbeConfig.Module,
			ScrapeTimeout: scrapeTimeout,
			ProberSpec: monitoringv1.ProberSpec{
				URL:    uptimeProbe.Spec.ProbeConfig.ProberUrl,
				Scheme: uptimeProbe.Spec.ProbeConfig.ProberScheme,
				Path:   uptimeProbe.Spec.ProbeConfig.ProberPath,
			},
			Targets: monitoringv1.ProbeTargets{
				StaticConfig: &monitoringv1.ProbeTargetStaticConfig{
					Targets: targetUrls,
				},
			},
		},
	}

	return r.Create(ctx, probe)
}

func (r *SpokeRouteReconciler) updateProbe(ctx context.Context, uptimeProbe *v1alpha1.UptimeProbe, route *unstructured.Unstructured, probe *monitoringv1.Probe, targetUrls []string) error {
	interval := r.getDurationFromAnnotation(route, "interval", uptimeProbe.Spec.ProbeConfig.Interval)
	scrapeTimeout := r.getDurationFromAnnotation(route, "scrapeTimeout", uptimeProbe.Spec.ProbeConfig.ScrapeTimeout)

	patchBase := client.MergeFrom(probe.DeepCopy())
	probe.Spec.JobName = uptimeProbe.Spec.ProbeConfig.JobName
	probe.Spec.Interval = interval
	probe.Spec.Module = uptimeProbe.Spec.ProbeConfig.Module
	probe.Spec.ScrapeTimeout = scrapeTimeout
	probe.Spec.ProberSpec = monitoringv1.ProberSpec{
		URL:    uptimeProbe.Spec.ProbeConfig.ProberUrl,
		Scheme: uptimeProbe.Spec.ProbeConfig.ProberScheme,
		Path:   uptimeProbe.Spec.ProbeConfig.ProberPath,
	}
	probe.Spec.Targets = monitoringv1.ProbeTargets{
		StaticConfig: &monitoringv1.ProbeTargetStaticConfig{
			Targets: targetUrls,
		},
	}

	return r.Patch(ctx, probe, patchBase)
}

func (r *SpokeRouteReconciler) getProbeName(route *unstructured.Unstructured) string {
	return fmt.Sprintf("%v-%v-%v", r.Name, route.GetNamespace(), route.GetName())
}

func (r *SpokeRouteReconciler) createOrUpdateProbe(ctx context.Context, uptimeProbe *v1alpha1.UptimeProbe, route *unstructured.Unstructured) error {
	probeName := r.getProbeName(route)

	targetUrls, err := r.getTargetUrls(route)
	if err != nil {
		r.logger.Info(err.Error())
		return nil
	}

	probe := &monitoringv1.Probe{}
	err = r.Get(ctx, types.NamespacedName{
		Name:      probeName,
		Namespace: uptimeProbe.Spec.ProbeConfig.TargetNamespace,
	}, probe)

	if errors.IsNotFound(err) {
		r.logger.Info(fmt.Sprintf("Probe %v not found, creating", probeName))
		if err := r.createProbe(ctx, uptimeProbe, route, probeName, targetUrls); err != nil {
			r.logger.Error(err, fmt.Sprintf("Error creating probe %v", probeName))
			return err
		}
		r.logger.Info(fmt.Sprintf("Created probe %v", probeName))
		return nil
	}

	if err != nil {
		r.logger.Error(err, fmt.Sprintf("Error getting probe %v", probeName))
		return err
	}

	r.logger.Info(fmt.Sprintf("Updating probe %v", probeName))
	if err := r.updateProbe(ctx, uptimeProbe, route, probe, targetUrls); err != nil {
		r.logger.Error(err, fmt.Sprintf("Error updating probe %v", probeName))
		return err
	}

	r.logger.Info(fmt.Sprintf("Updated probe %v", probeName))
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SpokeRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	routeGVR := schema.GroupVersionResource{
		Group:    "route.openshift.io",
		Version:  "v1",
		Resource: "routes",
	}

	factory := dynamicinformer.NewDynamicSharedInformerFactory(r.RemoteClient, 30*time.Second)
	informer := factory.ForResource(routeGVR).Informer()
	factory.WaitForCacheSync(r.Stop)
	factory.Start(r.Stop)

	return ctrl.NewControllerManagedBy(mgr).
		Named(r.Name).
		For(&v1alpha1.UptimeProbe{}).
		WatchesRawSource(&source.Informer{
			Informer: informer,
			Handler: handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
				var requests []reconcile.Request
				uptimeProbes := &v1alpha1.UptimeProbeList{}
				err := r.List(ctx, uptimeProbes, client.InNamespace(cache.AllNamespaces))
				if err != nil {
					return requests
				}

				for _, probe := range uptimeProbes.Items {
					selector := labels.SelectorFromSet(probe.Spec.LabelSelector.MatchLabels)
					if !selector.Matches(labels.Set(object.GetLabels())) {
						continue
					}

					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Namespace: probe.Namespace,
							Name:      probe.Name,
						},
					})
				}

				return requests
			}),
		}).
		Complete(r)
}
