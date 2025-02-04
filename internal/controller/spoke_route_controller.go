package controller

import (
	"context"
	"github.com/go-logr/logr"
	"github.com/stakater/UptimeGuardian/api/v1alpha1"
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
	"time"
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

	// Merge with UptimeProbeController

	// 1. Get the UptimeProbe CR with local client
	// 2. Get all routes matching labels
	// 3. Create/Update all Probes

	//routeGVR := schema.GroupVersionResource{
	//	Group:    "route.openshift.io",
	//	Version:  "v1",
	//	Resource: "routes",
	//}
	//
	//// Get the remote Route
	//route, err := r.RemoteClient.Resource(routeGVR).Namespace(req.Namespace).Get(ctx, req.Name, v1.GetOptions{})
	//if err != nil {
	//	if errors.IsNotFound(err) {
	//		return ctrl.Result{}, nil
	//	}
	//
	//	r.logger.Info("Failed to get remote Route: %v", err)
	//	return ctrl.Result{}, err
	//}
	//
	//r.logger.Info(fmt.Sprintf("%v", route.GetName()))

	return ctrl.Result{}, nil
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
