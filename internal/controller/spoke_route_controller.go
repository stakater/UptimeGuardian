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
	RemoteClient *dynamic.DynamicClient
	Stop         chan struct{}
	logger       logr.Logger
}

func (r *SpokeRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.FromContext(ctx).WithName(r.Name)
	r.logger.Info("Reconciling SpokeRoute")

	uptimeProbe := &v1alpha1.UptimeProbe{}
	if err := r.Get(ctx, req.NamespacedName, uptimeProbe); err != nil {
		return ctrl.Result{}, err
	}

	r.logger.Info(fmt.Sprintf("synching UptimeProbe:%v", uptimeProbe.GetName()))

	routes, err := r.getMatchingRoutes(ctx, uptimeProbe)
	if err != nil {
		return ctrl.Result{}, err
	}

	r.logger.Info(fmt.Sprintf("Found %v routes", len(routes.Items)))

	for _, route := range routes.Items {
		r.logger.Info(fmt.Sprintf("Processing route: %v", route.GetName()))
		if err := r.createOrUpdateProbe(ctx, uptimeProbe, &route); err != nil {
			r.logger.Error(err, fmt.Sprintf("Failed to process route %v", route.GetName()))
		}
	}

	return ctrl.Result{}, nil
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

func (r *SpokeRouteReconciler) createProbe(ctx context.Context, uptimeProbe *v1alpha1.UptimeProbe, route *unstructured.Unstructured, probeName string, targetUrls []string) error {
	interval := r.getDurationFromAnnotation(route, "interval", uptimeProbe.Spec.ProbeConfig.Interval)
	scrapeTimeout := r.getDurationFromAnnotation(route, "scrapeTimeout", uptimeProbe.Spec.ProbeConfig.ScrapeTimeout)

	probe := &monitoringv1.Probe{
		ObjectMeta: v1.ObjectMeta{
			Name:      probeName,
			Namespace: uptimeProbe.Spec.ProbeConfig.TargetNamespace,
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

func (r *SpokeRouteReconciler) createOrUpdateProbe(ctx context.Context, uptimeProbe *v1alpha1.UptimeProbe, route *unstructured.Unstructured) error {
	probeName := fmt.Sprintf("%v-%v-%v", r.Name, route.GetNamespace(), route.GetName())

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
					r.logger.Info(fmt.Sprintf("Processing uptimeProbe: %v", probe.GetName()))
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
