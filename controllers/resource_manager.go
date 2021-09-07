package controllers

import (

	//"golang.org/x/tools/godoc/redirect"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"

	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	//"golang.org/x/tools/godoc/redirect"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/retry"

	// "k8s.io/apiextensions-apiserver/pkg/client/clientset"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"

	"k8s.io/apimachinery/pkg/types"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
		auth, err = r.GetSecret(ctx, types.NamespacedName{
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
			r.Log.Info("Creating statefulset")
			stateful_set, create_sset_err = r.CreateStatefulSet(ctx, req, redisCluster.Spec, redisCluster.ObjectMeta.GetLabels(), config_map)
			if create_sset_err != nil {
				r.Log.Error(err, "Error when creating StatefulSet")
				return ctrl.Result{}, err
			}
			r.Log.Info("Successfully created statefulset", "statefulset", stateful_set)
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

	// check the cluster state and slots allocated. if states is ok, we can reset the status
	r.Log.Info("ReconcileClusterObject", "state", redisCluster.Status.Status)
	// Update slots ranges
	redisCluster.Status.Slots = r.GetSlotsRanges(redisCluster.Spec.Replicas)
	redisCluster.Status.Nodes, err = r.GetReadyNodes(ctx, redisCluster)

	desiredRedisClusterReplicas := redisCluster.Spec.Replicas
	statefulSetReplicas := *stateful_set.Spec.Replicas

	if desiredRedisClusterReplicas != statefulSetReplicas && redisCluster.Status.Status == v1alpha1.StatusReady {
		r.UpdateScalingStatus(ctx, redisCluster)
		// scale the cluster up or down
		if desiredRedisClusterReplicas > statefulSetReplicas {
			// SCALING UP
			// 1 Scale up (adjust statefulset)
			// Wait for ALL ready
			// Cluster meet
			// Rebalance

			// update status field on rdcl
			stateful_set.Spec.Replicas = &desiredRedisClusterReplicas
			r.Log.Info("ScaleCluster UP - updating statefulset replicas", "newsize", stateful_set.Spec.Replicas)

			// TODO: Make this update conflict safe
			err := r.Client.Update(ctx, stateful_set)
			if err != nil {
				return ctrl.Result{}, err
			}

			var readyNodes map[string]*v1alpha1.RedisNode
			var readyNodeCount int32 = 0
			for readyNodeCount != desiredRedisClusterReplicas {
				time.Sleep(2 * time.Second)
				readyNodes, err := r.GetReadyNodes(ctx, redisCluster)
				if err != nil {
					return ctrl.Result{}, err
				}
				readyNodeCount = int32(len(readyNodes))
			}
			r.Log.Info("Ready nodes count is:", "readynodes", readyNodeCount)
			for _, v := range readyNodes {
				r.Log.Info("Node: ", "node id", v.NodeID)
				r.Log.Info("NodeIP: ", "node ip", v.IP)
			}
			readyNodes, err = r.GetReadyNodes(ctx, redisCluster)
			if err != nil {
				return ctrl.Result{}, err
			}
			r.Log.Info("All nodes are ready. Running ClusterMeet")

			cmErr := r.ClusterMeet(ctx, readyNodes, redisCluster)
			if cmErr != nil {
				return ctrl.Result{}, cmErr
			}

			r.Log.Info("Rebalancing Cluster")
			rErr := r.RebalanceClusterRedisNativeScaleUp(ctx, redisCluster, readyNodes)
			if rErr != nil {
				return ctrl.Result{}, rErr
			}

			r.Log.Info("Rebalance done")

		} else if desiredRedisClusterReplicas < statefulSetReplicas {
			// down

			// Identify nodes to be forgotten
			// Rebalance cluster with to-be-forgotten weight=0
			// Forget nodes
			// Adjust statefulset
			r.Log.Info("ScaleCluster DOWN - ", "newsize", desiredRedisClusterReplicas)

			nodesToRemoveCount := statefulSetReplicas - desiredRedisClusterReplicas
			readyNodes, err := r.GetReadyNodes(ctx, redisCluster)
			readyNodesLength := int32(len(readyNodes))
			if err != nil {
				return ctrl.Result{}, err
			}
			//
			nodesToRemove := make(map[string]*v1alpha1.RedisNode, nodesToRemoveCount)
			var remainingNode *v1alpha1.RedisNode
			var counter int32 = 0
			var picked bool = false
			r.Log.Info("Determining nodes to-be-removed")
			for key, val := range readyNodes {
				counter++
				if counter > (readyNodesLength - nodesToRemoveCount) {
					nodesToRemove[key] = val
				} else if !picked {
					// pick one node that'll remain to use as command platform
					remainingNode = readyNodes[key]
					picked = true
				}
			}

			if nodesToRemoveCount != int32(len(nodesToRemove)) {
				r.Log.Info("ERROR finding nodes to remove!")
			}

			balanceErr := r.RebalanceClusterRedisNativeScaleDown(ctx, redisCluster, nodesToRemove, remainingNode)
			if balanceErr != nil {
				return ctrl.Result{}, balanceErr
			}

			r.ForgetUnnecessarySpecificNodes(ctx, redisCluster, nodesToRemove, remainingNode)

			stateful_set.Spec.Replicas = &desiredRedisClusterReplicas
			r.Log.Info("ScaleCluster UP - updating statefulset replicas", "newsize", stateful_set.Spec.Replicas)

			// TODO: Make this update conflict safe
			statefulSetUpdateErr := r.Client.Update(ctx, stateful_set)
			if err != nil {
				return ctrl.Result{}, statefulSetUpdateErr
			}
		}

		r.UpdateScalingStatus(ctx, redisCluster)
	}

	if err != nil {
		return ctrl.Result{}, err
	}
	requeue := false
	switch redisCluster.Status.Status {
	case v1alpha1.StatusConfiguring:
		requeue = true
		err := r.ConfigureRedisCluster(ctx, redisCluster)
		if err != nil {
			r.Log.Error(err, "Error when configuring cluster. Will retry again.")
			break
		}
		r.CheckConfigurationStatus(ctx, redisCluster)
	case v1alpha1.StatusReady:
		r.CheckConfigurationStatus(ctx, redisCluster)
		r.UpdateScalingStatus(ctx, redisCluster)
	// case v1alpha1.StatusScalingDown:
	// 	err := r.ScaleCluster(ctx, redisCluster)
	// 	if err != nil {
	// 		r.Log.Error(err, "Error when scaling down")
	// 		redisCluster.Status.Status = v1alpha1.StatusError
	// 		break
	// 	}
	// 	r.UpdateScalingStatus(ctx, redisCluster)
	// 	requeue = true
	// case v1alpha1.StatusScalingUp:
	// 	err := r.ScaleCluster(ctx, redisCluster)
	// 	if err != nil {
	// 		r.Log.Error(err, "Error when scaling up")
	// 		redisCluster.Status.Status = v1alpha1.StatusError
	// 		break
	// 	}
	// 	r.UpdateScalingStatus(ctx, redisCluster)
	// 	requeue = true
	case v1alpha1.StatusError:
		// todo: try to recover from error. Check configuration status?
		r.Recorder.Event(redisCluster, "Warning", "ClusterError", "Cluster error recorded.")
		r.CheckConfigurationStatus(ctx, redisCluster)
		err := r.ScaleCluster(ctx, redisCluster)
		if err == nil {
			r.UpdateScalingStatus(ctx, redisCluster)
		}
		requeue = true
	default:
		r.CheckConfigurationStatus(ctx, redisCluster)
	}

	var update_err error
	if !reflect.DeepEqual(redisCluster.Status, currentStatus) {
		update_err = r.UpdateClusterStatus(ctx, redisCluster)
	}

	if requeue {
		return ctrl.Result{RequeueAfter: time.Second * 5}, update_err
	}
	return ctrl.Result{}, update_err

}

func (r *RedisClusterReconciler) CheckConfigurationStatus(ctx context.Context, redisCluster *v1alpha1.RedisCluster) {
	clusterInfo := r.GetClusterInfo(ctx, redisCluster)
	state := clusterInfo["cluster_state"]
	slots_ok := clusterInfo["cluster_slots_ok"]
	slots_assigned := clusterInfo["cluster_slots_assigned"]
	readyNodes, _ := r.GetReadyNodes(ctx, redisCluster)
	r.Log.Info("CheckConfigurationStatus", "cluster_state", state, "cluster_slots_ok", slots_ok, "status", redisCluster.Status.Status, "clusterinfo", clusterInfo)
	if state == "ok" && slots_ok == strconv.Itoa(redis.TotalClusterSlots) {
		redisCluster.Status.Status = v1alpha1.StatusReady
	}
	if slots_ok == "0" || slots_ok == "" {
		if len(readyNodes) == int(redisCluster.Spec.Replicas) {
			redisCluster.Status.Status = v1alpha1.StatusConfiguring
		} else {
			redisCluster.Status.Status = v1alpha1.StatusInitializing
		}
		return
	}
	slots := r.GetClusterSlotConfiguration(ctx, redisCluster)
	nodeips := make(map[string]string, len(readyNodes))
	for id, node := range readyNodes {
		nodeips[node.IP] = id
	}
	// Check that ips in current configuration of slots have a corresponding node IP in pods
	r.Log.Info("CheckConfigurationStatus - checking slots ips matches", "ips", nodeips)
	for _, slotRange := range slots {
		addr := strings.Split(slotRange.Nodes[0].Addr, ":")
		slotnodeid := nodeips[addr[0]]
		if slotnodeid == "" {
			redisCluster.Status.Status = v1alpha1.StatusConfiguring
			r.Log.Info("CheckConfigurationStatus - slots configuration doesn't match ip address of a node, reconfiguration will apply", "expected_addr", slotRange.Nodes[0].Addr)
			return
		}
	}

	if slots_ok != slots_assigned {
		redisCluster.Status.Status = v1alpha1.StatusConfiguring
		r.Log.Info("CheckConfigurationStatus - slots assigned != slots  ok", "slots_ok", slots_ok, "slots_assigned", slots_assigned)
	}

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

	if spec.Labels != nil {
		for k, v := range *spec.Labels {
			statefulSet.Labels[k] = v
			statefulSet.Spec.Template.Labels[k] = v
		}

	}

	if spec.Resources != nil {
		for k := range statefulSet.Spec.Template.Spec.Containers {
			statefulSet.Spec.Template.Spec.Containers[k].Resources = *spec.Resources
		}
		return statefulSet, nil
	}

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
	cpuLimit, _ := resource.ParseQuantity("1")
	r.Log.Info("New memory limits", "memory", memoryLimit)
	memoryLimit.Add(memoryOverheadResource)
	for k := range statefulSet.Spec.Template.Spec.Containers {
		statefulSet.Spec.Template.Spec.Containers[k].Resources.Requests[corev1.ResourceMemory] = memoryLimit
		statefulSet.Spec.Template.Spec.Containers[k].Resources.Limits[corev1.ResourceMemory] = memoryLimit
		r.Log.Info("Stateful set container memory", "memory", statefulSet.Spec.Template.Spec.Containers[k].Resources.Limits[corev1.ResourceMemory])

		statefulSet.Spec.Template.Spec.Containers[k].Resources.Requests[corev1.ResourceCPU] = cpuLimit
		statefulSet.Spec.Template.Spec.Containers[k].Resources.Limits[corev1.ResourceCPU] = cpuLimit
		r.Log.Info("Stateful set cpu", "cpu", statefulSet.Spec.Template.Spec.Containers[k].Resources.Limits[corev1.ResourceCPU])

	}
	return statefulSet, nil
}

func (r *RedisClusterReconciler) CreateService(req ctrl.Request, labels map[string]string) *corev1.Service {
	return redis.CreateService(req.Namespace, req.Name, labels)
}

func (r *RedisClusterReconciler) GetSecret(ctx context.Context, ns types.NamespacedName) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	err := r.Client.Get(ctx, ns, secret)
	if err != nil {
		r.Log.Error(err, "Getting secret failed", "secret", ns)
	}
	return secret, err
}

/*

 */

func (r *RedisClusterReconciler) GetClusterInfo(ctx context.Context, redisCluster *v1alpha1.RedisCluster) map[string]string {
	if len(redisCluster.Status.Nodes) == 0 {
		r.Log.Info("No ready nodes available on the cluster.", "clusterinfo", map[string]string{})
		return map[string]string{}
	}
	nodes := r.GetRedisClusterPods(ctx, redisCluster)
	if len(nodes.Items) == 0 {
		return nil
	}
	secret, _ := r.GetRedisSecret(redisCluster)
	rdb := r.GetRedisClient(ctx, nodes.Items[0].Status.PodIP, secret)
	info, _ := rdb.ClusterInfo(ctx).Result()
	r.Log.Info("GetClusterInfo", "nodes", len(nodes.Items), "ip", nodes.Items[0].Status.PodIP, "info", info)
	parsedClusterInfo := redis.GetClusterInfo(info)
	return parsedClusterInfo
}

func (r *RedisClusterReconciler) UpdateClusterStatus(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {

	var req reconcile.Request
	req.NamespacedName.Namespace = redisCluster.Namespace
	req.NamespacedName.Name = redisCluster.Name

	r.Log.Info("Updating cluster status", "status", redisCluster.Status.Status, "nodes", redisCluster.Status.Nodes)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		time.Sleep(time.Second * 1)
		// get a fresh rediscluster to minimize conflicts
		refreshedRedisCluster := v1alpha1.RedisCluster{}
		err := r.Client.Get(ctx, types.NamespacedName{Namespace: redisCluster.Namespace, Name: redisCluster.Name}, &refreshedRedisCluster)
		if err != nil {
			r.Log.Error(err, "Error getting a refreshed RedisCluster before updating it. It may have been deleted?")
			return err
		}
		// update the slots
		refreshedRedisCluster.Status.Slots = redisCluster.Status.Slots
		refreshedRedisCluster.Status.Nodes = redisCluster.Status.Nodes
		refreshedRedisCluster.Status.Status = redisCluster.Status.Status
		var updateErr = r.Client.Status().Update(ctx, &refreshedRedisCluster)
		return updateErr
	})
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
