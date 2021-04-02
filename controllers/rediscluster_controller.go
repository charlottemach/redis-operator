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
	"context"

	"github.com/go-logr/logr"
	//"golang.org/x/tools/godoc/redirect"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/containersolutions/redis-operator/api/v1alpha1"
	redisv1alpha1 "github.com/containersolutions/redis-operator/api/v1alpha1"
	redis "github.com/containersolutions/redis-operator/internal/redis"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RedisClusterReconciler reconciles a RedisCluster object
type RedisClusterReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=redis.containersolutions.com,resources=redisclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=redis.containersolutions.com,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=redis.containersolutions.com,resources=redisclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=redis.containersolutions.com,resources=redisclusters/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the RedisCluster object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.2/pkg/reconcile
func (r *RedisClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("rediscluster", req.NamespacedName)

	// r.Log.Error(fmt.Errorf("sorry"), "Getting cluster data failed")
	// return ctrl.Result{}, fmt.Errorf("sorry")

	/*
			   get cluster state
			   get desired state
			   if (no stateful set found)
		{	     create stateful set
			     configure cluster
			     add slots

			   if statefulset and replicas incorrect
			     create replicaset

	*/
	redisCluster := &v1alpha1.RedisCluster{}
	err := r.Client.Get(ctx, req.NamespacedName, redisCluster)
	if err != nil {
		r.Log.Info("Deleting resources as the cluster is gone")
		if errors.IsNotFound(err) {
			// cleanup
			sset := r.CreateStatefulSet(ctx, req, 0)
			cmap := r.CreateConfigMap(req)
			r.Client.Delete(ctx, sset)
			r.Client.Delete(ctx, cmap)

		} else {
			r.Log.Error(err, "Getting cluster data failed")
		}
		return ctrl.Result{}, nil
	}

	err, sset := r.FindExistingStatefulSet(ctx, req)
	if err != nil {
		if errors.IsNotFound(err) {
			// create stateful set
			create_map_err := r.Client.Create(ctx, r.CreateConfigMap(req))
			if create_map_err != nil {
				return reconcile.Result{}, create_map_err
			}
			create_err := r.Client.Create(ctx, r.CreateStatefulSet(ctx, req, redisCluster.Spec.Replicas))
			if create_err != nil {
				return reconcile.Result{}, create_err
			}
		} else {
			r.Log.Error(err, "Getting statefulset data failed")
			return ctrl.Result{}, nil
		}
	}
	if sset.Spec.Replicas != &(redisCluster.Spec.Replicas) {
		// fix replicas
	}

	return ctrl.Result{}, nil

}

// SetupWithManager sets up the controller with the Manager.
func (r *RedisClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&redisv1alpha1.RedisCluster{}).
		Complete(r)
}

func (r *RedisClusterReconciler) FindExistingStatefulSet(ctx context.Context, req ctrl.Request) (error, *v1.StatefulSet) {
	sset := &v1.StatefulSet{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, sset)
	if err != nil {
		return err, nil
	}
	return nil, sset
}

func (r *RedisClusterReconciler) CreateConfigMap(req ctrl.Request) *corev1.ConfigMap {
	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
		Data: map[string]string{"redis.conf": "maxmemory 1600mb\nmaxmemory-samples 5\nmaxmemory-policy allkeys-lru\nappendonly no\nprotected-mode no\ndir /data\ncluster-enabled yes\ncluster-require-full-coverage no\ncluster-node-timeout 15000\ncluster-config-file /data/nodes.conf\ncluster-migration-barrier 1\n\n"},
	}
	return &cm
}

func (r *RedisClusterReconciler) CreateStatefulSet(ctx context.Context, req ctrl.Request, replicas int32) *v1.StatefulSet {
	return redis.CreateStatefulSet(ctx, req, replicas)
}
