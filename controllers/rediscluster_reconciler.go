/*
Copyright 2021.

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

package controllers

import (

	//"golang.org/x/tools/godoc/redirect"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"

	"context"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	finalizer "github.com/containersolutions/redis-operator/internal/finalizers"
	"github.com/go-logr/logr"

	//"golang.org/x/tools/godoc/redirect"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"

	// "k8s.io/apiextensions-apiserver/pkg/client/clientset"

	"k8s.io/apimachinery/pkg/runtime"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/containersolutions/redis-operator/api/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/source"
)

// RedisClusterReconciler reconciles a RedisCluster object
type RedisClusterReconciler struct {
	client.Client
	Log                     logr.Logger
	Scheme                  *runtime.Scheme
	Recorder                record.EventRecorder
	Finalizers              []finalizer.Finalizer
	MaxConcurrentReconciles int
}

//+kubebuilder:rbac:groups=redis.containersolutions.com,resources=redisclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=redis.containersolutions.com,resources=redisclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=redis.containersolutions.com,resources=redisclusters/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get
//+kubebuilder:rbac:groups=apps,resources=statefulsets;deployments,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=configmap;services;pods,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=configmaps;services,verbs=get;list;create;delete
//+kubebuilder:rbac:groups=apps,resources=statefulsets;deployments,verbs=create;delete;patch;update
func (r *RedisClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = r.Log.WithValues("rediscluster", req.NamespacedName)
	r.Log.Info("RedisCluster reconciler called", "name", req.Name, "ns", req.Namespace)
	redisCluster := &v1alpha1.RedisCluster{}
	err := r.Client.Get(ctx, req.NamespacedName, redisCluster)
	r.RefreshRedisClients(ctx, redisCluster)
	if err == nil {
		r.Log.Info("Found RedisCluster", "name", redisCluster.GetName(), "GVK", redisCluster.GroupVersionKind().String())
		return r.ReconcileClusterObject(ctx, req, redisCluster)
	} else {
		// cluster deleted
		r.Log.Info("Can't find RedisCluster, probably deleted")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *RedisClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RedisCluster{}).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, &handler.EnqueueRequestForObject{}, builder.WithPredicates(r.PreFilter())).
		Owns(&v1.StatefulSet{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10, Log: r.Log}).
		Complete(r)
}

func (r *RedisClusterReconciler) PreFilter() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !r.isOwnedByUs(e.ObjectNew) {
				return false
			}
			return true
		},
		CreateFunc: func(e event.CreateEvent) bool {
			if !r.isOwnedByUs(e.Object) {
				return false
			}
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			if !r.isOwnedByUs(e.Object) {
				return false
			}
			return true
		},
	}
}
