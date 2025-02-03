/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"

	"github.com/go-logr/logr"
	networkingv1alpha1 "github.com/stakater/UptimeGuardian/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// UptimeProbeReconciler reconciles a UptimeProbe object
type UptimeProbeReconciler struct {
	client.Client
	Scheme                    *runtime.Scheme
	logger                    logr.Logger
	SpokeClusterManagerConfig *networkingv1alpha1.UptimeProbe
}

//+kubebuilder:rbac:groups=networking.stakater.com,resources=uptimeprobes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.stakater.com,resources=uptimeprobes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=networking.stakater.com,resources=uptimeprobes/finalizers,verbs=update

func (r *UptimeProbeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.FromContext(ctx)
	r.logger.Info("Reconciling UptimeProbe")

	// Fetch UptimeProbe instance
	probe := &networkingv1alpha1.UptimeProbe{}
	if err := r.Get(ctx, req.NamespacedName, probe); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		// If not found, create the default singleton instance
		if req.Name == "default-uptime-probe" && req.Namespace == "test-1" {
			defaultProbe := networkingv1alpha1.DefaultUptimeProbe()
			if err := r.Create(ctx, defaultProbe); err != nil {
				r.logger.Error(err, "Failed to create default UptimeProbe")
				return ctrl.Result{}, err
			}
			r.logger.Info("Created default UptimeProbe")
			probe = defaultProbe
		} else {
			return ctrl.Result{}, nil
		}
	}

	// Update the SpokeClusterManagerConfig
	r.SpokeClusterManagerConfig = probe
	r.logger.Info("Updated SpokeClusterManagerConfig", "probe", probe.Name)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UptimeProbeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create the default singleton instance during setup
	ctx := context.Background()
	defaultProbe := networkingv1alpha1.DefaultUptimeProbe()
	if err := r.Create(ctx, defaultProbe); err != nil {
		if client.IgnoreAlreadyExists(err) != nil {
			r.logger.Error(err, "Failed to create default UptimeProbe during setup")
			return err
		}
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha1.UptimeProbe{}).
		Complete(r)
}
