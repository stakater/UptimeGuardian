package controller

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"
)

// SpokeRouteReconciler reconciles a SpokeRoute object
type SpokeRouteReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	logger       logr.Logger
	name         string
	remoteClient *dynamic.DynamicClient
	queue        workqueue.RateLimitingInterface
}

func (r *SpokeRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.FromContext(ctx).WithName(r.name)
	r.logger.Info("Reconciling SpokeRoute")

	routeGVR := schema.GroupVersionResource{
		Group:    "route.openshift.io",
		Version:  "v1",
		Resource: "routes",
	}

	// Get the remote Route
	route, err := r.remoteClient.Resource(routeGVR).Namespace(req.Namespace).Get(ctx, req.Name, v1.GetOptions{})
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

func (r *SpokeRouteReconciler) WatchRemoteRoutes() {
	routeGVR := schema.GroupVersionResource{
		Group:    "route.openshift.io",
		Version:  "v1",
		Resource: "routes",
	}

	factory := dynamicinformer.NewDynamicSharedInformerFactory(r.remoteClient, 30*time.Second)
	informer := factory.ForResource(routeGVR).Informer()
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			route, ok := obj.(*unstructured.Unstructured)
			if !ok {
				r.logger.Info("Failed to convert added object to Unstructured")
				return
			}
			r.queue.Add(reconcile.Request{
				NamespacedName: client.ObjectKey{
					Namespace: route.GetNamespace(),
					Name:      route.GetName(),
				},
			})
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			route, ok := newObj.(*unstructured.Unstructured)
			if !ok {
				r.logger.Info("Failed to convert updated object to Unstructured")
				return
			}
			r.queue.Add(reconcile.Request{
				NamespacedName: client.ObjectKey{
					Namespace: route.GetNamespace(),
					Name:      route.GetName(),
				},
			})
		},
		DeleteFunc: func(obj interface{}) {
			route, ok := obj.(*unstructured.Unstructured)
			if !ok {
				r.logger.Info("Failed to convert deleted object to Unstructured")
				return
			}
			r.logger.Info(fmt.Sprintf("Route deleted: %s/%s", route.GetNamespace(), route.GetName()))
		},
	})

	if err != nil {
		return
	}

	// Start informer
	stopCh := make(chan struct{})
	defer close(stopCh)
	informer.Run(stopCh)
}

func (r *SpokeRouteReconciler) ProcessQueue() {
	for {
		item, shutdown := r.queue.Get()
		if shutdown {
			r.logger.Info("Workqueue shutting down")
			return
		}

		req, ok := item.(reconcile.Request)
		if !ok {
			r.logger.Info("Failed to parse item from queue")
			r.queue.Done(item)
			continue
		}

		_, err := r.Reconcile(context.Background(), req)
		if err != nil {
			r.logger.Error(err, "Reconciliation failed")
			r.queue.AddRateLimited(req)
		} else {
			r.queue.Forget(item)
		}

		r.queue.Done(item)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *SpokeRouteReconciler) SetupWithManager(localClient client.Client, dc *dynamic.DynamicClient, name string) error {
	r.Client = localClient
	r.name = name
	r.remoteClient = dc
	r.queue = workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	go r.WatchRemoteRoutes()
	go r.ProcessQueue()

	return nil
}
