package controllers

import (

	//"golang.org/x/tools/godoc/redirect"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"

	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	//"golang.org/x/tools/godoc/redirect"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	// "k8s.io/apiextensions-apiserver/pkg/client/clientset"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"

	"k8s.io/apimachinery/pkg/types"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/containersolutions/redis-operator/api/v1alpha1"
	redis "github.com/containersolutions/redis-operator/internal/redis"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func (r *RedisClusterReconciler) GetRedisClusterName(o client.Object) string {
	r.Log.Info("GetRedisClusterName", "o.Kind", o.GetObjectKind().GroupVersionKind().Kind)
	if o.GetObjectKind().GroupVersionKind().Kind == "RedisCluster" {
		return o.GetName()
	}
	return o.GetLabels()[redis.RedisClusterLabel]
}

func (r *RedisClusterReconciler) GetRedisClusterNsName(o client.Object) string {
	r.Log.Info("GetRedisClusterName", "o.Kind", o.GetObjectKind().GroupVersionKind().Kind)
	if o.GetObjectKind().GroupVersionKind().Kind == "RedisCluster" {
		return o.GetNamespace() + "/" + o.GetName()
	}
	return o.GetNamespace() + "/" + o.GetLabels()[redis.RedisClusterLabel]
}

func (r *RedisClusterReconciler) ReconcileClusterObject(ctx context.Context, req ctrl.Request, redisCluster *v1alpha1.RedisCluster) (ctrl.Result, error) {
	currentStatus := redisCluster.Status
	var auth = &corev1.Secret{}
	var err error
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

	if !redisCluster.GetDeletionTimestamp().IsZero() {
		for _, f := range r.Finalizers {
			if containsString(redisCluster.GetFinalizers(), f.GetId()) {
				r.Log.Info("Running finalizer", "id", f.GetId(), "finalizer", f)
				finalizerError := f.DeleteMethod(ctx, redisCluster, r.Client)
				if finalizerError != nil {
					r.Log.Error(finalizerError, "Finalizer returned error", "id", f.GetId(), "finalizer", f)
				}
				controllerutil.RemoveFinalizer(redisCluster, f.GetId())
				if err := r.Update(ctx, redisCluster); err != nil {
					return ctrl.Result{}, err
				}
			}
		}
	}

	err, config_map := r.FindExistingConfigMap(ctx, req)

	if err != nil {
		if errors.IsNotFound(err) {
			config_map = r.CreateConfigMap(req, redisCluster.Spec, auth, redisCluster.GetObjectMeta().GetLabels())
			ctrl.SetControllerReference(redisCluster, config_map, r.Scheme)
			r.Log.Info("Creating configmap", "configmap", config_map)
			create_map_err := r.Client.Create(ctx, config_map)
			if create_map_err != nil {
				r.Log.Error(create_map_err, "Error when creating configmap")
				return ctrl.Result{}, create_map_err
			}
		} else {
			r.Log.Error(err, "Getting configmap data failed")
		}
	}

	err, stateful_set := r.FindExistingStatefulSet(ctx, req)
	var create_sset_err error
	if err != nil {
		if errors.IsNotFound(err) {
			// set status to Initializing
			// create stateful set
			stateful_set, create_sset_err = r.CreateStatefulSet(ctx, req, redisCluster.Spec, redisCluster.ObjectMeta.GetLabels(), config_map)
			if create_sset_err != nil {
				r.Log.Error(err, "Error when creating StatefulSet")
				return ctrl.Result{}, err
			}
			ctrl.SetControllerReference(redisCluster, stateful_set, r.Scheme)
			create_sset_err = r.Client.Create(ctx, stateful_set)
			if create_sset_err != nil && errors.IsAlreadyExists(create_sset_err) {
				r.Log.Info("StatefulSet already exists")
			}
			if redisCluster.Spec.Monitoring != nil {
				mdep := r.CreateMonitoringDeployment(ctx, req, redisCluster, redisCluster.ObjectMeta.GetLabels())
				ctrl.SetControllerReference(redisCluster, mdep, r.Scheme)
				mdep_create_err := r.Client.Create(ctx, mdep)
				if mdep_create_err != nil && errors.IsAlreadyExists(create_sset_err) {
					r.Log.Info("Monitoring pod already exists")
				} else if mdep_create_err != nil {
					r.Log.Error(mdep_create_err, "Error creating monitoring deployment")
				}
			}

		} else {
			r.Log.Error(err, "Getting statefulset data failed")

		}
	}
	create_svc_err, service := r.FindExistingService(ctx, req)
	if create_svc_err != nil {
		if errors.IsNotFound(create_svc_err) {
			service = r.CreateService(req, redisCluster.GetObjectMeta().GetLabels())
			ctrl.SetControllerReference(redisCluster, service, r.Scheme)
			create_svc_err := r.Client.Create(ctx, service)
			if create_svc_err != nil && errors.IsAlreadyExists(create_svc_err) {
				r.Log.Info("Svc already exists")
			} else if create_svc_err != nil {
				return ctrl.Result{}, create_svc_err
			}
		} else {
			r.Log.Error(err, "Getting svc data failed")

		}
	}
	r.UpdateInternalObjectReference(config_map, r.GetRedisClusterNsName(redisCluster))
	r.UpdateInternalObjectReference(stateful_set, r.GetRedisClusterNsName(redisCluster))
	r.UpdateInternalObjectReference(service, r.GetRedisClusterNsName(redisCluster))
	r.RefreshResources(ctx, client.Object(redisCluster))

	r.Log.Info("Checking if cluster status can be updated to Ready")
	// check the cluster state and slots allocated. if states is ok, we can reset the status
	clusterInfo := r.GetClusterInfo(ctx, redisCluster)
	r.Log.Info("Cluster info", "clusterinfo", clusterInfo)
	state := clusterInfo["cluster_state"]
	slots_ok := clusterInfo["cluster_slots_ok"]

	if state == "ok" && slots_ok == "16384" && redisCluster.Status.Status == v1alpha1.StatusConfiguring {
		r.Log.Info("Cluster state to Ready")
		redisCluster.Status.Status = v1alpha1.StatusReady
	} else {
		r.Log.Info("Cluster state to Configuring")
		redisCluster.Status.Status = v1alpha1.StatusConfiguring
	}
	redisCluster.Status.Nodes = r.GetReadyNodes(ctx, redisCluster.Name)
	var update_err error
	if !reflect.DeepEqual(redisCluster.Status, currentStatus) {
		update_err = r.UpdateClusterStatus(ctx, redisCluster)
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, update_err

}

// TODO: swap return values
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
	configStringMap := redis.ConfigStringToMap(config)
	labels[redis.RedisClusterLabel] = req.Name
	if val, exists := secret.Data["requirepass"]; exists {
		configStringMap["requirepass"] = string(val)
	} else if secret.Name != "" {
		r.Log.Info("requirepass field not found in secret", "secretdata", secret.Data)
	}

	withDefaults := redis.MergeWithDefaultConfig(configStringMap)

	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels:    labels,
		},
		Data: map[string]string{"redis.conf": redis.MapToConfigString(withDefaults), "memory-overhead": "300Mi"},
	}

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
			Template: *rediscluster.Spec.Monitoring,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{redis.RedisClusterLabel: req.Name, "app": "monitoring"},
			},
			Replicas: pointer.Int32Ptr(1),
		},
	}
	if labels == nil {
		d.Spec.Template.Labels = make(map[string]string)
	} else {
		d.Spec.Template.Labels = labels
	}
	d.Spec.Template.Labels[redis.RedisClusterLabel] = req.Name
	d.Spec.Template.Labels["app"] = "monitoring"
	for k, v := range rediscluster.Spec.Monitoring.Labels {
		d.Spec.Template.Labels[k] = v
	}

	return d
}

func (r *RedisClusterReconciler) CreateStatefulSet(ctx context.Context, req ctrl.Request, spec v1alpha1.RedisClusterSpec, labels map[string]string, configmap *corev1.ConfigMap) (*v1.StatefulSet, error) {
	statefulSet := redis.CreateStatefulSet(ctx, req, spec, labels)
	config := spec.Config
	configStringMap := redis.ConfigStringToMap(config)
	withDefaults := redis.MergeWithDefaultConfig(configStringMap)
	maxMemory := strings.ToLower(withDefaults["maxmemory"])
	r.Log.Info("Merged config", "withDefaults", withDefaults)
	maxMemoryInt, err := redis.ConvertRedisMemToMbytes(maxMemory)

	memoryOverheadConfig := configmap.Data["maxmemory-overhead"]
	var memoryOverheadResource resource.Quantity

	if memoryOverheadConfig == "" {
		memoryOverheadResource = resource.MustParse("300Mi")
	} else {
		memoryOverheadResource = resource.MustParse(memoryOverheadConfig)
	}

	if err != nil {
		return nil, err
	}
	memoryLimit, _ := resource.ParseQuantity(fmt.Sprintf("%dMi", maxMemoryInt)) // add 300 mb from config maxmemory
	r.Log.Info("New memory limits", "memory", memoryLimit)
	memoryLimit.Add(memoryOverheadResource)
	for k := range statefulSet.Spec.Template.Spec.Containers {
		statefulSet.Spec.Template.Spec.Containers[k].Resources.Requests[corev1.ResourceMemory] = memoryLimit
		statefulSet.Spec.Template.Spec.Containers[k].Resources.Limits[corev1.ResourceMemory] = memoryLimit
		r.Log.Info("Stateful set container memory", "memory", statefulSet.Spec.Template.Spec.Containers[k].Resources.Limits[corev1.ResourceMemory])
	}
	return statefulSet, nil
}

func (r *RedisClusterReconciler) CreateService(req ctrl.Request, labels map[string]string) *corev1.Service {
	return redis.CreateService(req.Namespace, req.Name, labels)
}

func (r *RedisClusterReconciler) GetSecret(ctx context.Context, ns types.NamespacedName) (error, *corev1.Secret) {
	secret := &corev1.Secret{}
	err := r.Client.Get(ctx, ns, secret)
	if err != nil {
		r.Log.Error(err, "Getting secret failed", "secret", ns)
	}
	return err, secret
}

/*
   FindInternalResource uses any client.Object instance to try to find a Redis cluster that it belongs to.
   It can accept StatefulSet, ConfigMap, Service, etc.
*/
func (r *RedisClusterReconciler) FindInternalResource(ctx context.Context, o client.Object, into client.Object) error {
	r.Log.Info("FindInternalResource", "o", r.GetObjectKey(o))
	ns := types.NamespacedName{
		Name:      r.GetRedisClusterName(o),
		Namespace: o.GetNamespace(),
	}
	err := r.Client.Get(ctx, ns, into)
	return err
}

/*

 */

func (r *RedisClusterReconciler) GetClusterInfo(ctx context.Context, redisCluster *v1alpha1.RedisCluster) map[string]string {
	if len(redisCluster.Status.Nodes) == 0 {
		r.Log.Info("No ready nodes availableo on the cluster.", "clusterinfo", map[string]string{})
		return map[string]string{}
	}
	node := redisCluster.Status.Nodes[0]
	secret, _ := r.GetRedisSecret(redisCluster)
	rdb := r.GetRedisClient(ctx, node.IP, secret)
	info, _ := rdb.ClusterInfo(ctx).Result()
	return redis.GetClusterInfo(info)
}

func (r *RedisClusterReconciler) UpdateClusterStatus(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {
	r.Log.Info("Updating cluster status", "status", redisCluster.Status.Status, "nodes", redisCluster.Status.Nodes)
	err := r.Client.Status().Update(ctx, redisCluster)
	if err != nil {
		r.Log.Error(err, "Error updating cluster status")
	}
	return err
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
