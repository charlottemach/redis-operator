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
	"fmt"

	"github.com/go-logr/logr"
	//"golang.org/x/tools/godoc/redirect"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	// "k8s.io/apiextensions-apiserver/pkg/client/clientset"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/containersolutions/redis-operator/api/v1alpha1"
	redis "github.com/containersolutions/redis-operator/internal/redis"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
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
	r.Log.Info("RedisCluster reconciler called", "name", req.Name, "ns", req.Namespace)

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
		if errors.IsNotFound(err) {
			r.Client.Delete(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: req.Namespace},
			})
		}
		r.Log.Info("The cluster has been deleted")
		return ctrl.Result{}, nil
	}
	var auth = &corev1.Secret{}
	if len(redisCluster.Spec.Auth.SecretName) > 0 {
		err, auth = r.GetSecret(ctx, types.NamespacedName{
			Name:      redisCluster.Spec.Auth.SecretName,
			Namespace: req.Namespace,
		})
		if err != nil {
			r.Log.Error(err, "Can't find provided secret", "redisCluster", redisCluster)
			return ctrl.Result{}, nil
		}
	}
	err, _ = r.FindExistingConfigMap(ctx, req)

	if err != nil {
		if errors.IsNotFound(err) {
			cmap := r.CreateConfigMap(req, redisCluster.Spec, auth, redisCluster.GetObjectMeta().GetLabels())
			ctrl.SetControllerReference(redisCluster, cmap, r.Scheme)
			r.Log.Info("Creating configmap", "configmap", cmap)
			create_map_err := r.Client.Create(ctx, cmap)
			if create_map_err != nil {
				r.Log.Error(create_map_err, "Error when creating configmap")
			}
		} else {
			r.Log.Error(err, "Getting configmap data failed")
		}
	}
	err, sset := r.FindExistingStatefulSet(ctx, req)
	if err != nil {
		if errors.IsNotFound(err) {
			// create stateful set
			sset := r.CreateStatefulSet(ctx, req, redisCluster.Spec, redisCluster.ObjectMeta.GetLabels())
			ctrl.SetControllerReference(redisCluster, sset, r.Scheme)
			create_err := r.Client.Create(ctx, sset)
			if create_err != nil && errors.IsAlreadyExists(create_err) {
				r.Log.Info("StatefulSet already exists")
			}
			// if redisCluster.Spec.MonitoringTemplate  {
			mdep := r.CreateMonitoringDeployment(ctx, req, redisCluster, redisCluster.ObjectMeta.GetLabels())
			ctrl.SetControllerReference(redisCluster, mdep, r.Scheme)
			mdep_create_err := r.Client.Create(ctx, mdep)
			if mdep_create_err != nil && errors.IsAlreadyExists(create_err) {
				r.Log.Info("Monitoring pod already exists")
			} else if mdep_create_err != nil {
				r.Log.Error(mdep_create_err, "Error creating monitoring deployment")
			}
			// }

		} else {
			r.Log.Error(err, "Getting statefulset data failed")

		}
	}
	err, _ = r.FindExistingService(ctx, req)
	if err != nil {
		if errors.IsNotFound(err) {
			svc := r.CreateService(req, redisCluster.GetObjectMeta().GetLabels())
			ctrl.SetControllerReference(redisCluster, svc, r.Scheme)
			create_svc_err := r.Client.Create(ctx, svc)
			if create_svc_err != nil && errors.IsAlreadyExists(create_svc_err) {
				r.Log.Info("Svc already exists")
			}
		} else {
			r.Log.Error(err, "Getting svc data failed")

		}
	}
	if sset != nil && sset.Spec.Replicas != &(redisCluster.Spec.Replicas) {
		// fix replicas
		// return
	}

	// Instance are set up and replica count is sufficient

	// check that all instances aware of each other

	return ctrl.Result{}, nil

}

func (r *RedisClusterReconciler) GetSecret(ctx context.Context, ns types.NamespacedName) (error, *corev1.Secret) {
	secret := &corev1.Secret{}
	err := r.Client.Get(ctx, ns, secret)
	if err != nil {
		r.Log.Error(err, "Getting secret failed", "secret", ns)
	}
	return err, secret
}

// SetupWithManager sets up the controller with the Manager.
func (r *RedisClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RedisCluster{}).
		Owns(&v1.StatefulSet{}).
		Owns(&corev1.ConfigMap{}).
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

func (r *RedisClusterReconciler) FindExistingConfigMap(ctx context.Context, req ctrl.Request) (error, *corev1.ConfigMap) {
	cmap := &corev1.ConfigMap{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, cmap)
	if err != nil {
		return err, nil
	}
	return nil, cmap
}

func (r *RedisClusterReconciler) FindExistingService(ctx context.Context, req ctrl.Request) (error, *corev1.Service) {
	svc := &corev1.Service{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: req.Namespace}, svc)
	if err != nil {
		return err, nil
	}
	return nil, svc
}

func (r *RedisClusterReconciler) CreateConfigMap(req ctrl.Request, spec v1alpha1.RedisClusterSpec, secret *corev1.Secret, labels map[string]string) *corev1.ConfigMap {
	config := spec.Config
	r.Log.Info("spec config", "sc", spec.Config)
	if config == "" {
		config = "maxmemory 1600mb\nmaxmemory-samples 5\nmaxmemory-policy allkeys-lru\nappendonly yes\nprotected-mode no\ndir /data\ncluster-enabled yes\ncluster-require-full-coverage no\ncluster-node-timeout 15000\ncluster-config-file /data/nodes.conf\ncluster-migration-barrier 1\n"
	}
	r.Log.Info("d config", "config", config)
	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels:    labels,
		},
		Data: map[string]string{"redis.conf": config},
	}
	if val, exists := secret.Data["requirepass"]; exists {
		cm.Data["redis.conf"] = cm.Data["redis.conf"] + fmt.Sprintf("\nrequirepass \"%s\"\n", val)
	} else if secret.Name != "" {
		r.Log.Info("requirepass field not found in secret", "secretdata", secret.Data)
	}
	// add cluster config file entries
	cm.Data["redis.conf"] = cm.Data["redis.conf"] + "cluster-config-file /data/nodes.conf\n"
	cm.Data["redis.conf"] = cm.Data["redis.conf"] + "dir /data/\n"
	cm.Data["redis.conf"] = cm.Data["redis.conf"] + "cluster-enabled yes\n"
	cm.Data["redis.conf"] = cm.Data["redis.conf"] + "cluster-require-full-coverage no\n"
	cm.Data["redis.conf"] = cm.Data["redis.conf"] + "cluster-migration-barrier 1\n"

	r.Log.Info("Generated Configmap", "configmap", cm)
	r.Log.Info("Spec config", "speconfig", spec.Config)
	return &cm
}

func (r *RedisClusterReconciler) CreateMonitoringDeployment(ctx context.Context, req ctrl.Request, rediscluster *v1alpha1.RedisCluster, labels map[string]string) *v1.Deployment {
	d := &v1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name + "-monitoring",
			Namespace: req.Namespace,
			Labels:    labels,
		},
		Spec: v1.DeploymentSpec{
			Template: rediscluster.Spec.Monitoring,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"rediscluster": req.Name, "app": "monitoring"},
			},
			Replicas: pointer.Int32Ptr(1),
		},
	}
	if labels == nil {
		d.Spec.Template.Labels = make(map[string]string)
	} else {
		d.Spec.Template.Labels = labels
	}
	d.Spec.Template.Labels["rediscluster"] = req.Name
	d.Spec.Template.Labels["app"] = "monitoring"
	for k, v := range rediscluster.Spec.Monitoring.Labels {
		d.Spec.Template.Labels[k] = v
	}

	return d
}

func (r *RedisClusterReconciler) CreateStatefulSet(ctx context.Context, req ctrl.Request, spec v1alpha1.RedisClusterSpec, labels map[string]string) *v1.StatefulSet {
	return redis.CreateStatefulSet(ctx, req, spec, labels)
}

func (r *RedisClusterReconciler) CreateService(req ctrl.Request, labels map[string]string) *corev1.Service {
	return redis.CreateService(req.Namespace, req.Name, labels)
}
