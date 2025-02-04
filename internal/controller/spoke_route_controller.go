package controller

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	route "github.com/openshift/api/route/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"time"
)

// SpokeRouteReconciler reconciles a SpokeRoute object
type SpokeRouteReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Name         string
	RemoteClient *dynamic.DynamicClient
	logger       logr.Logger
}

func (r *SpokeRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.FromContext(ctx).WithName(r.Name)
	r.logger.Info("Reconciling SpokeRoute")

	routeGVR := schema.GroupVersionResource{
		Group:    "route.openshift.io",
		Version:  "v1",
		Resource: "routes",
	}

	// Get the remote Route
	route, err := r.RemoteClient.Resource(routeGVR).Namespace(req.Namespace).Get(ctx, req.Name, v1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		r.logger.Info("Failed to get remote Route: %v", err)
		return ctrl.Result{}, err
	}

	r.logger.Info(fmt.Sprintf("%v", route.GetName()))
	return ctrl.Result{}, nil
}

func (r *SpokeRouteReconciler) getInformer() cache.SharedIndexInformer {
	routeGVR := schema.GroupVersionResource{
		Group:    "route.openshift.io",
		Version:  "v1",
		Resource: "routes",
	}

	factory := dynamicinformer.NewDynamicSharedInformerFactory(r.RemoteClient, 30*time.Second)
	return factory.ForResource(routeGVR).Informer()
}

// SetupWithManager sets up the controller with the Manager.
func (r *SpokeRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(r.Name).
		Watches(&route.Route{}, &handler.EnqueueRequestForObject{}).
		WatchesRawSource(&source.Informer{
			Informer: r.getInformer(),
			Handler:  &handler.EnqueueRequestForObject{},
		}).
		Complete(r)
}
