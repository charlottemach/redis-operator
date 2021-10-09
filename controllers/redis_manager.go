package controllers

import (
	"errors"
	v1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	//"golang.org/x/tools/godoc/redirect"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"

	"context"
	"fmt"

	redisclient "github.com/go-redis/redis/v8"

	//"golang.org/x/tools/godoc/redirect"

	corev1 "k8s.io/api/core/v1"

	// "k8s.io/apiextensions-apiserver/pkg/client/clientset"

	"k8s.io/apimachinery/pkg/types"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/containersolutions/redis-operator/api/v1alpha1"
	redis "github.com/containersolutions/redis-operator/internal/redis"
	"k8s.io/apimachinery/pkg/labels"
)

type RedisClient struct {
	NodeId      string
	RedisClient *redisclient.Client
	IP          string
}

type MigrationResult struct {
	Error error
}

type SlotMigration struct {
	Dst  string
	Src  string
	Slot int
}

var redisClients map[string]*RedisClient = make(map[string]*RedisClient)

func (r *RedisClusterReconciler) RefreshRedisClients(ctx context.Context, redisCluster *v1alpha1.RedisCluster) {
	nodes, _ := r.GetReadyNodes(ctx, redisCluster)
	for nodeId, node := range nodes {
		secret, _ := r.GetRedisSecret(redisCluster)
		err := r.GetRedisClient(ctx, node.IP, secret).Ping(ctx).Err()
		if err != nil {
			r.Log.Info("RefreshRedisClients - Redis client for node is errorring", "node", nodeId, "error", err)
			r.GetRedisClient(ctx, node.IP, secret).Close()
			redisClients[nodeId] = nil
		}
	}
}

func (r *RedisClusterReconciler) GetRedisClientForNode(ctx context.Context, nodeId string, redisCluster *v1alpha1.RedisCluster) (*redisclient.Client, error) {
	nodes, _ := r.GetReadyNodes(ctx, redisCluster)
	// If redisClient for this node has not been initialized, or the IP has changed
	if nodes[nodeId] == nil {
		return nil, fmt.Errorf("Node %s does not exist", nodeId)
	}
	if redisClients[nodeId] == nil || redisClients[nodeId].IP != nodes[nodeId].IP {
		secret, _ := r.GetRedisSecret(redisCluster)
		rdb := r.GetRedisClient(ctx, nodes[nodeId].IP, secret)
		redisClients[nodeId] = &RedisClient{NodeId: nodeId, RedisClient: rdb, IP: nodes[nodeId].IP}
	}

	return redisClients[nodeId].RedisClient, nil
}

func (r *RedisClusterReconciler) RemoveRedisClientForNode(nodeId string, ctx context.Context, redisCluster *v1alpha1.RedisCluster) {
	if redisClients[nodeId] == nil {
		return
	}
	redisClients[nodeId].RedisClient.Close()
	redisClients[nodeId] = nil
}

func (r *RedisClusterReconciler) ConfigureRedisCluster(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {
	readyNodes, _ := r.GetReadyNodes(ctx, redisCluster)
	r.Log.Info("ConfigureRedisCluster", "readyNodes", readyNodes, "equality", reflect.DeepEqual(readyNodes, redisCluster.Status.Nodes))
	err := r.ClusterMeet(ctx, readyNodes, redisCluster)
	if err != nil {
		r.Recorder.Event(redisCluster, "Warning", "ClusterMeet", "Error when attempting ClusterMeet ")
		return err
	}
	r.Recorder.Event(redisCluster, "Normal", "ClusterMeet", "Redis cluster meet completed.")

	err = r.AssignSlots(ctx, readyNodes, redisCluster)
	if err != nil {
		r.Recorder.Event(redisCluster, "Warning", "SlotAssignment", "Error when attempting AssignSlots ")
		return err
	}
	r.Recorder.Event(redisCluster, "Normal", "SlotAssignment", "Slot assignment execution complete")

	return nil
}

func (r *RedisClusterReconciler) UpdateScalingStatus(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {
	sset_err, sset := r.FindExistingStatefulSet(ctx, controllerruntime.Request{NamespacedName: types.NamespacedName{Name: redisCluster.Name, Namespace: redisCluster.Namespace}})
	if sset_err != nil {
		return sset_err
	}
	currSsetReplicas := *(sset.Spec.Replicas)
	if redisCluster.Spec.Replicas < currSsetReplicas {
		r.Recorder.Event(redisCluster, "Normal", "ClusterScalingDown", "Redis cluster scaling down required.")
		redisCluster.Status.Status = v1alpha1.StatusScalingDown
	}

	if redisCluster.Spec.Replicas > currSsetReplicas {
		// change status to
		r.Recorder.Event(redisCluster, "Normal", "ClusterScalingUp", "Redis cluster scaling up required.")
		redisCluster.Status.Status = v1alpha1.StatusScalingUp
	}
	if redisCluster.Spec.Replicas == currSsetReplicas {
		if redisCluster.Status.Status == v1alpha1.StatusScalingDown {
			// forget all scaled down nodes
			r.Recorder.Event(redisCluster, "Normal", "ClusterReady", "Redis cluster scaled down.")
			redisCluster.Status.Status = v1alpha1.StatusReady
		}
		if redisCluster.Status.Status == v1alpha1.StatusScalingUp {
			r.Recorder.Event(redisCluster, "Normal", "ClusterScalingUp", "Redis cluster scaling up.")
			if len(redisCluster.Status.Nodes) == int(currSsetReplicas) {
				redisCluster.Status.Status = v1alpha1.StatusReady
			}
		}
	}

	return nil
}

func (r *RedisClusterReconciler) UpdateUpgradingStatus(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {
	err, statefulSet := r.FindExistingStatefulSet(ctx, controllerruntime.Request{NamespacedName: types.NamespacedName{Name: redisCluster.Name, Namespace: redisCluster.Namespace}})
	if err != nil {
		return err
	}

	err, configMap := r.FindExistingConfigMap(ctx, controllerruntime.Request{NamespacedName: types.NamespacedName{Name: redisCluster.Name, Namespace: redisCluster.Namespace}})
	if err != nil {
		return err
	}

	// Check whether the redis configuration has changed,
	// as this will constitute a cluster upgrade
	desiredConfig := redis.MergeWithDefaultConfig(redis.ConfigStringToMap(redisCluster.Spec.Config))
	observedConfig := redis.ConfigStringToMap(configMap.Data["redis.conf"])
	if !reflect.DeepEqual(observedConfig, desiredConfig) {
		r.Log.Info("Cluster Upgrade Issued", "reason", "Redis Config Changed")
		redisCluster.Status.Status = v1alpha1.StatusUpgrading
		return nil
	}

	// Check whether any upgradeable object changes have been made.
	// Any change in these attributes or properties will constitute a live upgrade.
	desiredResources := *(redisCluster.Spec.Resources)
	observedResources := statefulSet.Spec.Template.Spec.Containers[0].Resources
	if !reflect.DeepEqual(observedResources, desiredResources) {
		r.Log.Info("Cluster Upgrade Issued", "reason", "Redis Resource Requests & Limits Changed", "observed", observedResources, "desired", desiredResources)
		redisCluster.Status.Status = v1alpha1.StatusUpgrading
		return nil
	}

	// Check whether any upgradeable object changes have been made.
	// Any change in these attributes or properties will constitute a live upgrade.
	desiredImage := redisCluster.Spec.Image
	observedImage := statefulSet.Spec.Template.Spec.Containers[0].Image
	if observedImage != desiredImage {
		r.Log.Info("Cluster Upgrade Issued", "reason", "Redis Image Changed", "observed", observedImage, "desired", desiredImage)
		redisCluster.Status.Status = v1alpha1.StatusUpgrading
		return nil
	}

	// Temporary line to force cluster upgrade
	//redisCluster.Status.Status = v1alpha1.StatusUpgrading
	redisCluster.Status.Status = v1alpha1.StatusReady

	return nil
}

func (r *RedisClusterReconciler) UpgradeCluster(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {
	err, configMap := r.FindExistingConfigMap(ctx, controllerruntime.Request{NamespacedName: types.NamespacedName{Name: redisCluster.Name, Namespace: redisCluster.Namespace}})
	if err != nil {
		return err
	}
	configMap.Data["redis.conf"] = redis.MapToConfigString(redis.MergeWithDefaultConfig(redis.ConfigStringToMap(redisCluster.Spec.Config)))
	r.Log.Info("Updating configmap", "configmap", configMap)
	err = r.Client.Update(ctx, configMap)
	if err != nil {
		r.Log.Error(err, "Error when creating configmap")
		return err
	}

	req := controllerruntime.Request{NamespacedName: types.NamespacedName{Name: redisCluster.Name, Namespace: redisCluster.Namespace}}

	statefulSet, err := r.CreateStatefulSet(ctx, req, redisCluster.Spec, redisCluster.ObjectMeta.GetLabels(), configMap)
	if err != nil {
		return err
	}

	var localPartition int32
	for partition := int(*(statefulSet.Spec.Replicas)) - 1; partition >= 0; partition-- {
		r.Log.Info("Looping paritions", "partition", partition)
		//	r.Log.Info("Upgrading with partition", "partition", partition)

		// First we need to rebalance away from this node
		podName := types.NamespacedName{Name: fmt.Sprintf("%s-%d", statefulSet.Name, partition), Namespace: redisCluster.Namespace}
		pod := corev1.Pod{}
		err = r.Client.Get(ctx, podName, &pod)
		if err != nil {
			r.Log.Error(err, "Could not find pod for partition node")
		}

		redisSecret, _ := r.GetRedisSecret(redisCluster)
		redisClient := r.GetRedisClient(ctx, pod.Status.PodIP, redisSecret)
		defer redisClient.Close()
		nodeId, err := redisClient.Do(ctx, "cluster", "myid").Result()
		if err != nil {
			r.Log.Error(err, "Could not find node id for pod")
		}
		weights := map[string]int{}
		weights[nodeId.(string)] = 0
		err = r.RebalanceCluster(ctx, redisCluster, weights)
		if err != nil {
			r.Log.Error(err, "Could not move slots away from node")
		}

		localPartition = int32(partition)
		statefulSet.Spec.UpdateStrategy = v1.StatefulSetUpdateStrategy{
			Type:          v1.RollingUpdateStatefulSetStrategyType,
			RollingUpdate: &v1.RollingUpdateStatefulSetStrategy{Partition: &localPartition},
		}
		statefulSet, err = r.UpdateStatefulSet(ctx, statefulSet)
		if err != nil {
			r.Log.Error(err, "Could not update partition for Statefulset")
		}

		// Let's wait for the StatefulSet to start recreating the pod before we check for it's readiness
		time.Sleep(time.Second*5)

		var current int
		err = wait.Poll(5*time.Second, 5*time.Minute,
			func() (bool, error) {
				pods := &corev1.PodList{}
				labelSelector := labels.SelectorFromSet(
					map[string]string{
						redis.RedisClusterLabel: redisCluster.Name,
						"app":                   "redis",
					},
				)

				err = r.Client.List(ctx, pods, &client.ListOptions{
					Namespace:     redisCluster.Namespace,
					LabelSelector: labelSelector,
				})
				if err != nil {
					r.Log.Error(err, "Could not wait for pods to become ready")
					return false, err
				}
				current = 0
				for _, pod := range pods.Items {
					flag, err := r.PodRunningReady(&pod)
					fmt.Println("Checking pod condition", flag, err)

					if err == nil && flag == true {
						current++
						fmt.Println("Increasing current count", current)
					}
				}
				if current != int(*(statefulSet.Spec.Replicas)) {
					r.Log.Info(fmt.Sprintf("Got %v pods running and ready, expect: %v", current, int(*(statefulSet.Spec.Replicas))))
					return false, nil
				}
				return true, nil
			})
		if err != nil {
			r.Log.Error(err, "Could not upgrade cluster")
		}
		//r.UpdateRedisCluster(ctx, redisCluster)
		//	r.Log.Info("Upgrade completed for partition", "partition", partition)
	}

	// We want to sleep to make sure the k8s client gets a chance to update the pod list,
	// before we try to rebalance again.
	time.Sleep(5*time.Second)
	// Now for one last rebalance to make sure everything is up and running correctly.
	err = r.RebalanceCluster(ctx, redisCluster, map[string]int{})

	redisCluster.Status.Status = v1alpha1.StatusReady

	return nil
}

// PodRunningReady checks whether pod p's phase is running and it has a ready
// condition of status true.
func (r *RedisClusterReconciler) PodRunningReady(p *corev1.Pod) (bool, error) {
	// Check the phase is running.
	if p.Status.Phase != corev1.PodRunning {
		return false, fmt.Errorf("want pod '%s' on '%s' to be '%v' but was '%v'",
			p.ObjectMeta.Name, p.Spec.NodeName, corev1.PodRunning, p.Status.Phase)
	}
	// Check the ready condition is true.
	_, condition := r.GetPodCondition(&p.Status, corev1.PodReady)
	fmt.Println(condition, condition.Type, condition.Status)
	if !(condition != nil && condition.Status == corev1.ConditionTrue) {
		return false, fmt.Errorf("pod '%s' on '%s' didn't have condition {%v %v}; conditions: %v",
			p.ObjectMeta.Name, p.Spec.NodeName, corev1.PodReady, corev1.ConditionTrue, p.Status.Conditions)
	}
	return true, nil
}

func (r *RedisClusterReconciler) GetPodCondition(status *corev1.PodStatus, conditionType corev1.PodConditionType) (int, *corev1.PodCondition) {
	if status == nil {
		return -1, nil
	}
	for i := range status.Conditions {
		if status.Conditions[i].Type == conditionType {
			return i, &status.Conditions[i]
		}
	}
	return -1, nil
}

func (r *RedisClusterReconciler) UpdateStatefulSet(ctx context.Context, statefulSet *v1.StatefulSet) (*v1.StatefulSet, error) {
	refreshedStatefulSet := &v1.StatefulSet{}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		time.Sleep(time.Second * 1)
		// get a fresh rediscluster to minimize conflicts
		err := r.Client.Get(ctx, types.NamespacedName{Namespace: statefulSet.Namespace, Name: statefulSet.Name}, refreshedStatefulSet)
		if err != nil {
			r.Log.Error(err, "Error getting a refreshed RedisCluster before updating it. It may have been deleted?")
			return err
		}
		// update the slots
		refreshedStatefulSet.Spec = statefulSet.Spec
		var updateErr = r.Client.Update(ctx, refreshedStatefulSet)
		return updateErr
	})
	if err != nil {
		return nil, err
	}
	return refreshedStatefulSet, nil
}

func (r *RedisClusterReconciler) UpdateRedisCluster(ctx context.Context, redisCluster *v1alpha1.RedisCluster) (*v1alpha1.RedisCluster, error) {
	refreshedRedisCluster := &v1alpha1.RedisCluster{}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		time.Sleep(time.Second * 1)
		// get a fresh rediscluster to minimize conflicts
		err := r.Client.Get(ctx, types.NamespacedName{Namespace: redisCluster.Namespace, Name: redisCluster.Name}, refreshedRedisCluster)
		if err != nil {
			r.Log.Error(err, "Error getting a refreshed RedisCluster before updating it. It may have been deleted?")
			return err
		}
		// update the slots
		refreshedRedisCluster.Spec = redisCluster.Spec
		refreshedRedisCluster.Status = redisCluster.Status
		var updateErr = r.Client.Status().Update(ctx, refreshedRedisCluster)
		return updateErr
	})
	if err != nil {
		return nil, err
	}
	return refreshedRedisCluster, nil
}

func (r *RedisClusterReconciler) ScaleCluster(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {
	var err error
	sset_err, sset := r.FindExistingStatefulSet(ctx, controllerruntime.Request{NamespacedName: types.NamespacedName{Name: redisCluster.Name, Namespace: redisCluster.Namespace}})
	if sset_err != nil {
		return err
	}

	// @TODO Cleanup
	readyNodes, err := r.GetReadyNodes(ctx, redisCluster)
	if err != nil {
		return err
	}

	nodeList, err := r.GetClusterNodes(ctx, redisCluster)
	if err != nil {
		return err
	}

	currSsetReplicas := *(sset.Spec.Replicas)
	redisCluster.Status.Slots = r.GetSlotsRanges(redisCluster.Spec.Replicas)
	// scaling down: if data migration takes place, move slots
	if redisCluster.Spec.Replicas < currSsetReplicas {
		// Scaling Down.
		// We need to find the pods which are going to be removed so we can rebalance away from them
		r.Log.Info("redisCluster.Spec.Replicas < statefulset.Spec.Replicas, downscaling", "rc.s.r", redisCluster.Spec.Replicas, "sset.s.r", currSsetReplicas)

		cullList := map[string]bool{}
		for i := int(redisCluster.Spec.Replicas); i <= int(currSsetReplicas-1); i++ {
			// Create a hash table of all the nodes to cull
			cullList[fmt.Sprintf("%s-%d", redisCluster.Name, i)] = true
		}

		weights := map[string]int{}
		for _, node := range readyNodes {
			if _, exists := cullList[node.NodeName]; exists {
				// Node should be culled
				weights[node.NodeID] = 0
			}
		}
		err = r.RebalanceCluster(ctx, redisCluster, weights)
		if err != nil {
			r.Log.Error(err, "Issues with rebalancing cluster when scaling down")
			return err
		}

		for weighted, weight := range weights {
			if weight == 0 {
				delete(nodeList, weighted)
			}
			// If we've removed it from balancing, we should probably remove it from the cluster
		}
		//
		//// Now we can forget all the other nodes for all the nodes
		for _, node := range nodeList {
			for weighted, weight := range weights {
				if weight == 0 {
					// If we've removed it from balancing, we should probably remove it from the cluster
					_, err := node.ClusterForgetNodeID(weighted)
					if err != nil {
						r.Log.Error(err, "Could not forget node")
						return err
					}
				}
			}
		}
	}
	//  scaling up and all pods became ready
	if int(redisCluster.Spec.Replicas) == len(readyNodes) {
		r.Log.Info("ScaleCluster - len(nodes) == replicas. Running forget unnecessary nodes, clustermeet, rebalance")

		err := r.ClusterMeet(ctx, readyNodes, redisCluster)
		if err != nil {
			r.Log.Error(err, "Cloud not join cluster")
			return err
		}
		// We want to wait for the meet to propogate through the Redis Cluster
		time.Sleep(10 * time.Second)
		err = r.RebalanceCluster(ctx, redisCluster, map[string]int{})

		if err != nil {
			r.Log.Error(err, "ScaleCluster - issue with rebalancing cluster when scaling up")
			return err
		}
	}

	newSize := redisCluster.Spec.Replicas

	// If we scaled up, we need to reload the statefulset,
	// as it'sbeen a while since we loaded it, and the status could have changed.
	r.Log.Info("ScaleCluster - updating statefulset replicas", "newsize", newSize)
	err, sset = r.FindExistingStatefulSet(ctx, controllerruntime.Request{NamespacedName: types.NamespacedName{Name: redisCluster.Name, Namespace: redisCluster.Namespace}})
	if err != nil {
		return err
	}
	sset.Spec.Replicas = &newSize
	err = r.Client.Update(ctx, sset)
	if err != nil {
		r.Log.Error(err, "Failed to update StatefulSet")
		return err
	}
	return nil
}

func (r *RedisClusterReconciler) isOwnedByUs(o client.Object) bool {
	labels := o.GetLabels()
	if _, found := labels[redis.RedisClusterLabel]; found {
		return true
	}
	return false
}

func (r *RedisClusterReconciler) ClusterMeet(ctx context.Context, nodes map[string]*v1alpha1.RedisNode, redisCluster *v1alpha1.RedisCluster) error {
	r.Log.Info("ClusterMeet", "nodes", nodes)
	var rdb *redisclient.Client

	for srcNodeId, srcnode := range nodes {
		for trgNodeId, trgnode := range nodes {
			if trgNodeId == srcNodeId {
				continue
			}
			r.Log.Info("ClusterMeet", "srcnode", srcnode, "trgnode", trgnode)
			rdb, _ = r.GetRedisClientForNode(ctx, srcNodeId, redisCluster)
			_, err := rdb.ClusterMeet(ctx, trgnode.IP, strconv.Itoa(redis.RedisCommPort)).Result()

			if err != nil {
				r.Log.Error(err, "ClusterMeet failed", "nodes", srcnode)
				return err
			}
		}

	}
	return nil
}

func (r *RedisClusterReconciler) GetSlotsRanges(nodes int32) []*v1alpha1.SlotRange {
	slots := redis.SplitNodeSlots(int(nodes))
	var apiRedisSlots = make([]*v1alpha1.SlotRange, 0)
	for _, node := range slots {
		apiRedisSlots = append(apiRedisSlots, &v1alpha1.SlotRange{Start: node.Start, End: node.End})
	}
	r.Log.Info("GetSlotsRanges", "slots", slots, "ranges", apiRedisSlots)
	return apiRedisSlots
}

func (r *RedisClusterReconciler) GetAnyRedisClient(ctx context.Context, redisCluster *v1alpha1.RedisCluster) (*redisclient.Client, error) {
	nodes, _ := r.GetReadyNodes(ctx, redisCluster)
	var client *redisclient.Client
	var err error
	for _, n := range nodes {
		client, err = r.GetRedisClientForNode(ctx, n.NodeID, redisCluster)
		if err != nil {
			return nil, err
		}
		break
	}
	return client, nil
}

func (r *RedisClusterReconciler) GetClusterSlotConfiguration(ctx context.Context, redisCluster *v1alpha1.RedisCluster) []redisclient.ClusterSlot {
	client, _ := r.GetAnyRedisClient(ctx, redisCluster)
	if client == nil {
		return nil
	}
	clusterSlots := client.ClusterSlots(ctx).Val()
	// todo: error handling
	return clusterSlots
}

func (r *RedisClusterReconciler) NodesBySequence(nodes map[string]*v1alpha1.RedisNode) ([]*v1alpha1.RedisNode, error) {
	nodesBySequence := make([]*v1alpha1.RedisNode, len(nodes))
	for _, node := range nodes {
		nodeNameElements := strings.Split(node.NodeName, "-")
		nodePodSequence, err := strconv.Atoi(nodeNameElements[len(nodeNameElements)-1])
		if err != nil {
			return nil, err
		}
		if len(nodes) <= nodePodSequence {
			return nil, fmt.Errorf("Race condition with pod sequence: seq:%d, butlen: %d", nodePodSequence, len(nodes))
		}
		nodesBySequence[nodePodSequence] = node
	}
	return nodesBySequence, nil
}

func (r *RedisClusterReconciler) ForgetUnnecessaryNodes(ctx context.Context, redisCluster *v1alpha1.RedisCluster) {
	readyNodes, _ := r.GetReadyNodes(ctx, redisCluster)
	remainingNodes := make([]*v1alpha1.RedisNode, 0)
	overstayedTheirWelcomeNodes := make([]*v1alpha1.RedisNode, 0)
	nodesBySequence, _ := r.NodesBySequence(readyNodes)
	for seq, node := range nodesBySequence {
		if seq < len(redisCluster.Status.Slots) {
			remainingNodes = append(remainingNodes, node)
		} else {
			overstayedTheirWelcomeNodes = append(overstayedTheirWelcomeNodes, node)
		}
	}
	for _, remainingNode := range remainingNodes {
		for _, forgottenNode := range overstayedTheirWelcomeNodes {
			client, err := r.GetRedisClientForNode(ctx, remainingNode.NodeID, redisCluster)
			if err != nil {
				r.Log.Error(err, "Node forget failed", "target", remainingNode, "forgottenNode", forgottenNode)
				continue
			}
			err = client.Do(ctx, "cluster", "forget", forgottenNode.NodeID).Err()
			if err != nil {
				r.Log.Error(err, "Node forget failed", "target", remainingNode, "forgottenNode", forgottenNode)
			}

		}
	}
	for _, node := range overstayedTheirWelcomeNodes {
		r.RemoveRedisClientForNode(node.NodeID, ctx, redisCluster)
	}
}

func (r *RedisClusterReconciler) GetClusterNodes(ctx context.Context, redisCluster *v1alpha1.RedisCluster) (map[string]*redis.ClusterNode, error) {
	nodeList := make(map[string]*redis.ClusterNode, 0)
	nodes, err := r.GetReadyNodes(ctx, redisCluster)
	if err != nil {
		r.Log.Error(err, "Could not get ready nodes for rebalancing")
	}
	for _, node := range nodes {
		clusterNode := redis.NewClusterNode(fmt.Sprintf("%s:%s", node.IP, "6379"))
		err := clusterNode.LoadInfo(false)
		if err != nil {
			r.Log.Error(err, "Failed to load Cluster Node Info", "node", clusterNode)
		}
		nodeList[clusterNode.Name()] = clusterNode
	}
	return nodeList, nil
}

func (r *RedisClusterReconciler) MoveSlot(ctx context.Context, redisCluster *v1alpha1.RedisCluster, source, target *redis.ClusterNode, nodes map[string]*redis.ClusterNode, slot int) error {
	_, err := source.Call("CLUSTER", "setslot", slot, "migrating", target.Name()).Text()
	if err != nil {
		r.Log.Error(err, "Failed to set slot migrating", "node", source.Name(), "target", target.Name())
		return err
	}
	_, err = target.Call("CLUSTER", "setslot", slot, "importing", source.Name()).Text()
	if err != nil {
		r.Log.Error(err, "Failed to set slot importing", "node", target.Name(), "source", source.Name())
		return err
	}

	for {
		keys, err := source.R().ClusterGetKeysInSlot(context.TODO(), slot, 100).Result()
		if err != nil {
			r.Log.Error(err, "Cannot fetch keys", "node", source.Name())
			return err
		}

		if len(keys) == 0 {
			break
		}

		if redisCluster.Spec.PurgeKeysOnRebalance {
			// XXX: migrate parameters check
			var cmd []interface{}

			cmd = append(cmd, "DEL")
			for _, key := range keys {
				cmd = append(cmd, key)
			}
			_, err := source.Call(cmd...).Result()
			if err != nil {
				if !strings.Contains(err.Error(), "ASK") {
					// If we get an ask redirection,
					// it means that the cluster wants us to ask the new node most probably.
					// When deleting keys, we do it to make the migration faster.
					// If we run against the new node, there is no good reason for deleting the keys anymore,
					// as the primary purpose of the delete has been served.
					// We can therefor ignore the ASK redirection, and simply continue to the next piece of logic.
					// In this case we got a different error, and definitely want to fail hard and fast
					// to void putting the Redis cluster in an unfixable state.
					r.Log.Error(err, "Failed to delete keys", "cmd", cmd)
					return err
				}
			}
		} else {
			// XXX: migrate parameters check
			var cmd []interface{}

			cmd = append(cmd, "migrate", target.Host(), target.Port(), "", 0, MIGRATE_TIMOUT, "KEYS")
			for _, key := range keys {
				cmd = append(cmd, key)
			}
			_, err = source.Call(cmd...).Text()

			if err != nil {
				// We want this error to propgate through, as we have a secondary test to try fixing the error
				r.Log.Error(err, "Migrate Failed", "keys", keys, "node", target.Name())
			}
			if err != nil {
				errinfo := err.Error()
				if strings.Contains(errinfo, "BUSYKEY") {
					// XXX: migrate parameters check
					cmd = cmd[:0]
					cmd = append(cmd, "migrate", target.Host(), target.Port(), "", 0, MIGRATE_TIMOUT, "REPLACE", "KEYS")
					for _, key := range keys {
						cmd = append(cmd, key)
					}
					err = source.Call(cmd...).Err()
					if err != nil {
						r.Log.Error(err, "Could not move keys to new node", "keys", keys, "source", source.Name(), "target", target.Name())
						return err
					}
				}
			}
		}
	}

	for _, node := range nodes {
		err := node.Call("CLUSTER", "setslot", slot, "node", target.Name()).Err()
		if err != nil {
			r.Log.Error(err, "Could not set new node for slot", "slot", slot, "node", node.Name(), "target", target.Name())
			return err
		}
	}
	return nil
}

func (r *RedisClusterReconciler) EnsureClusterSlotsStable(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {
	nodeList, err := r.GetClusterNodes(ctx, redisCluster)

	if err != nil {
		r.Log.Error(err, "Could not fetch cluster nodes")
		return err
	}

	// Case 1: Slot is in migrating on one node, and importing in another, but might have keys left.
	// Let's try to move the slot.
	for _, node := range nodeList {
		r.Log.Info("Fixing incomplete migration", "node", node.Name(), "importing", node.Importing())
		for slot, sourceId := range node.Importing() {
			err = r.MoveSlot(ctx, redisCluster, nodeList[sourceId], nodeList[node.Name()], nodeList, slot)
			if err != nil {
				r.Log.Error(err, "Cannot move slot", "slot", slot, "source", sourceId, "target", node.Name())
				return err
			}
		}
	}

	// We've tried to cover the most common cases in https://github.com/redis/redis/blob/unstable/src/redis-cli.c#L5012
	// If we find more cases, we can add them here as well.
	return nil
}

func (r *RedisClusterReconciler) RebalanceCluster(ctx context.Context, redisCluster *v1alpha1.RedisCluster, weights map[string]int) error {
	r.Log.Info("Rebalancing Cluster")
	nodeList := make(map[string]*redis.ClusterNode, 0)
	nodes, err := r.GetReadyNodes(ctx, redisCluster)
	if err != nil {
		r.Log.Error(err, "Could not get ready nodes for rebalancing")
	}
	for nodeId, node := range nodes {
		clusterNode := redis.NewClusterNode(fmt.Sprintf("%s:%s", node.IP, "6379"))
		err := clusterNode.LoadInfo(false)
		if err != nil {
			r.Log.Error(err, "Failed to load cluster node info", "node", nodeId)
			return err
		}
		nodeList[clusterNode.Name()] = clusterNode
	}
	slotMap := make(map[string][]int)
	for _, node := range nodeList {
		slotMap[node.Name()] = node.Slots()
	}
	moveMapOptions := NewMoveMapOptions()
	moveMapOptions.weights = weights
	slotMoveMap := CalculateSlotMoveMap(slotMap, moveMapOptions)
	slotMoveSequence := CalculateMoveSequence(slotMap, slotMoveMap, moveMapOptions)

	executeMoveSequence := func(waitGroup *sync.WaitGroup, moveSequence MoveSequence) error {
		defer waitGroup.Done()
		source := nodeList[moveSequence.From]
		target := nodeList[moveSequence.To]
		for _, slot := range moveSequence.Slots {
			err := r.MoveSlot(ctx, redisCluster, source, target, nodeList, slot)
			if err != nil {
				r.Log.Error(err, "Cannot move slot", "slot", slot, "source", source.Name(), "target", target.Name())
				return err
			}
		}
		return nil
	}

	var waitGroup sync.WaitGroup
	for _, moveSequence := range slotMoveSequence {
		waitGroup.Add(1)
		go executeMoveSequence(&waitGroup, moveSequence)
	}

	r.Log.Info("Waiting for slot moves", "nodeCount", len(slotMoveSequence))
	waitGroup.Wait()
	r.Log.Info("Done waiting for slot moves")
	err = r.EnsureClusterSlotsStable(ctx, redisCluster)
	if err != nil {
		r.Log.Error(err, "Cannot fix slots")
		return err
	}

	return nil
}

func (r *RedisClusterReconciler) GetSlotOwnerCandidate(slot int, nodesBySequence []*v1alpha1.RedisNode, redisCluster *v1alpha1.RedisCluster) (string, error) {
	slotsMap := redisCluster.Status.Slots
	for k, slotRange := range slotsMap {
		if slot <= slotRange.End && slot >= slotRange.Start {
			if len(nodesBySequence) <= k || nodesBySequence[k] == nil {
				return "", fmt.Errorf("Expected slot to be in a node sequence %d, however no such pod exists", k)
			}
			return nodesBySequence[k].NodeID, nil
		}
	}
	return "", nil
}

//TODO: check how many cluster slots have been already assign, and rebalance cluster if necessary
func (r *RedisClusterReconciler) AssignSlots(ctx context.Context, nodes map[string]*v1alpha1.RedisNode, redisCluster *v1alpha1.RedisCluster) error {
	// when all nodes are formed in a cluster, addslots
	r.Log.Info("AssignSlots", "nodeslen", len(nodes), "nodes", nodes)
	slots := redis.SplitNodeSlots(len(nodes))
	nodesBySequence, _ := r.NodesBySequence(nodes)
	for i, node := range nodesBySequence {
		rdb, err := r.GetRedisClientForNode(ctx, node.NodeID, redisCluster)
		if err != nil {
			return err
		}

		rdb.ClusterAddSlotsRange(ctx, slots[i].Start, slots[i].End)
		r.Log.Info("Running cluster assign slots", "pods", nodes)
	}
	return nil
}

func (r *RedisClusterReconciler) GetRedisClient(ctx context.Context, ip string, secret string) *redisclient.Client {
	redisclient.NewClusterClient(&redisclient.ClusterOptions{})
	rdb := redisclient.NewClient(&redisclient.Options{
		Addr:     fmt.Sprintf("%s:%d", ip, redis.RedisCommPort),
		Password: secret,
		DB:       0,
	})
	return rdb
}

func (r *RedisClusterReconciler) GetRedisClusterPods(ctx context.Context, redisCluster *v1alpha1.RedisCluster) *corev1.PodList {
	allPods := &corev1.PodList{}
	labelSelector := labels.SelectorFromSet(
		map[string]string{
			redis.RedisClusterLabel: redisCluster.Name,
			"app":                   "redis",
		},
	)

	r.Client.List(ctx, allPods, &client.ListOptions{
		Namespace:     redisCluster.Namespace,
		LabelSelector: labelSelector,
	})

	return allPods
}

func (r *RedisClusterReconciler) GetReadyNodes(ctx context.Context, redisCluster *v1alpha1.RedisCluster) (map[string]*v1alpha1.RedisNode, error) {
	allPods := &corev1.PodList{}
	labelSelector := labels.SelectorFromSet(
		map[string]string{
			redis.RedisClusterLabel: redisCluster.GetName(),
			"app":                   "redis",
		},
	)

	r.Client.List(ctx, allPods, &client.ListOptions{
		Namespace:     redisCluster.Namespace,
		LabelSelector: labelSelector,
	})
	readyNodes := make(map[string]*v1alpha1.RedisNode, 0)
	redisSecret, _ := r.GetRedisSecret(redisCluster)
	for _, pod := range allPods.Items {
		for _, s := range pod.Status.Conditions {
			if s.Type == corev1.PodReady && s.Status == corev1.ConditionTrue {
				// get node id
				redisClient := r.GetRedisClient(ctx, pod.Status.PodIP, redisSecret)
				defer redisClient.Close()
				nodeId, err := redisClient.Do(ctx, "cluster", "myid").Result()
				if err != nil {
					r.Log.Error(err, "Could not fetch node id", "pod", pod.Status.PodIP)
				}
				if nodeId == nil {
					return nil, errors.New("Can't fetch node id")
				}

				readyNodes[nodeId.(string)] = &v1alpha1.RedisNode{IP: pod.Status.PodIP, NodeName: pod.GetName(), NodeID: nodeId.(string)}
			}
		}
	}

	return readyNodes, nil
}

func (r *RedisClusterReconciler) GetRedisSecret(redisCluster *v1alpha1.RedisCluster) (string, error) {
	if redisCluster.Spec.Auth.SecretName == "" {
		return "", nil
	}

	secret := &corev1.Secret{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: redisCluster.Spec.Auth.SecretName, Namespace: redisCluster.Namespace}, secret)
	if err != nil {
		return "", err
	}
	redisSecret := string(secret.Data["requirepass"])
	return redisSecret, nil
}

func makeRange(min int, max int) []int {
	a := make([]int, max-min+1)
	for i := range a {
		a[i] = min + i
	}
	return a
}

type MoveMapOptions struct {
	threshhold int
	weights    map[string]int
}

func (opt *MoveMapOptions) GetNodeWeight(nodeId string) int {
	if weight, ok := opt.weights[nodeId]; ok {
		return weight
	}
	return 1
}

func NewMoveMapOptions() *MoveMapOptions {
	return &MoveMapOptions{
		threshhold: 2,
		weights:    make(map[string]int),
	}
}

func CalculateSlotMoveMap(slotMap map[string][]int, options *MoveMapOptions) map[string][]int {
	result := make(map[string][]int)

	totalSlots := 0
	totalWeight := 0
	for node, slots := range slotMap {
		totalSlots += len(slots)
		totalWeight += options.GetNodeWeight(node)
	}
	slotsPerWeight := int(math.Floor(float64(totalSlots / totalWeight)))
	for node, slots := range slotMap {
		weight := options.GetNodeWeight(node)
		if weight == 0 {
			// If the node is marked as 0 weight, don't even consider threshold, remove everything immediately
			result[node] = slots
		}

		shouldHaveSlots := slotsPerWeight * weight
		nodeShouldSend := len(slots) > (shouldHaveSlots + (shouldHaveSlots * options.threshhold / 100))
		if nodeShouldSend {
			result[node] = slots[shouldHaveSlots:]
		} else {
			result[node] = make([]int, 0)
		}
	}

	return result
}

type MoveSequence struct {
	From  string
	To    string
	Slots []int
}

func CalculateMoveSequence(SlotMap map[string][]int, SlotMoveMap map[string][]int, options *MoveMapOptions) []MoveSequence {
	hashMap := make(map[string]MoveSequence)

	totalSlots := 0
	totalWeight := 0
	for node, slots := range SlotMap {
		totalSlots += len(slots)
		totalWeight += options.GetNodeWeight(node)
	}
	slotsPerWeight := int(math.Floor(float64(totalSlots / totalWeight)))

	// Leftover string protects against rounding errors where a 0 weighted node could still have slots.
	// The actual leftover is the "last node with weight > 0", where we can move any leftover
	// slots to to protect 0 weighted slots
	var leftOverNode string
	for destination, destinationSlots := range SlotMap {
		destinationWeight := options.GetNodeWeight(destination)
		if destinationWeight == 0 {
			continue
		}
		destinationShouldHaveSlots := slotsPerWeight * destinationWeight
		if len(destinationSlots) < destinationShouldHaveSlots {
			// Destination needs slots
			needsSlots := destinationShouldHaveSlots - len(destinationSlots)
			for source, slots := range SlotMoveMap {
				if source == destination {
					// No point trying to take slots from ourself
					continue
				}
				if len(slots) == 0 {
					// No point trying to steal slots from poor sources
					continue
				}

				var takeSlots []int
				if len(slots) <= needsSlots {
					takeSlots = slots
					SlotMoveMap[source] = make([]int, 0)
				} else {
					takeSlots = slots[:needsSlots]
					SlotMoveMap[source] = slots[needsSlots:]
				}
				key := source + ":" + destination
				if _, ok := hashMap[key]; ok {
					hashMap[key] = MoveSequence{
						From:  hashMap[key].From,
						To:    hashMap[key].To,
						Slots: append(hashMap[key].Slots, takeSlots...),
					}
				} else {
					hashMap[key] = MoveSequence{
						From:  source,
						To:    destination,
						Slots: takeSlots,
					}
				}
				needsSlots -= len(takeSlots)
				if needsSlots == 0 {
					break
				}
			}
			leftOverNode = destination
		}
	}
	for source, slots := range SlotMoveMap {
		// We need to move any slots into the last destination node with weight > 1
		if len(slots) > 0 {
			if source == leftOverNode {
				// No point trying to take slots from ourself, we might as well leave them there
				SlotMoveMap[source] = []int{}
				continue
			}
			key := source + ":" + leftOverNode
			if _, ok := hashMap[key]; ok {
				hashMap[key] = MoveSequence{
					From:  hashMap[key].From,
					To:    hashMap[key].To,
					Slots: append(hashMap[key].Slots, slots...),
				}
			} else {
				hashMap[key] = MoveSequence{
					From:  source,
					To:    leftOverNode,
					Slots: slots,
				}
			}

		}
	}

	result := make([]MoveSequence, 0)

	for _, moveSequence := range hashMap {
		result = append(result, moveSequence)
	}

	return result
}
