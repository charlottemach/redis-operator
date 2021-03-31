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

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/containersolutions/redis-operator/api/v1alpha1"
	redisv1alpha1 "github.com/containersolutions/redis-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RedisClusterReconciler reconciles a RedisCluster object
type RedisClusterReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

type RedisClusterStatefulSet struct {
	RedisCluster v1alpha1.RedisCluster
	StatefulSet  v1.StatefulSet
}

func (r *RedisClusterStatefulSet) GetName() string {
	return r.RedisCluster.Name + "-cluster-inst"
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
	rcs := RedisClusterStatefulSet{RedisCluster: *redisCluster}
	err := r.Client.Get(ctx, req.NamespacedName, redisCluster)
	if err != nil {
		r.Log.Error(err, "Getting cluster data failed")
		return ctrl.Result{}, nil
	}

	err, sset := r.FindExistingStatefulSet(ctx, req)
	if err != nil {
		if errors.IsNotFound(err) {
			// create stateful set
			r.CreateStatefulSet(ctx, &rcs)

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
	err := r.Client.Get(ctx, req.NamespacedName, sset)
	if err != nil {
		r.Log.Error(err, "Can't find StatefulSet")
		return err, sset
	}
	return nil, sset
}

func (r *RedisClusterReconciler) CreateStatefulSet(ctx context.Context, rcs *RedisClusterStatefulSet) *v1.StatefulSet {
	redisStatefulSet := &v1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rcs.GetName(),
			Namespace: rcs.RedisCluster.Namespace,
		},
		Spec: v1.StatefulSetSpec{
			Replicas: &rcs.RedisCluster.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"rediscluster": rcs.RedisCluster.Name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"rediscluster": rcs.RedisCluster.Name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "redis-inst",
							Image: "redis:5.0.5",
							Ports: []corev1.ContainerPort{
								{
									Name:          "client",
									ContainerPort: 6379,
								},
								{
									Name:          "gossip",
									ContainerPort: 16379,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "config",
									MountPath: "/conf",
								},
								{
									Name:      "data",
									MountPath: "/data",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}
	return redisStatefulSet
}
