package controllers

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
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

func (r *RedisClusterReconciler) GetRedisClientForNode(ctx context.Context, nodeId string, redisCluster *v1alpha1.RedisCluster) *redisclient.Client {
	nodes, _ := r.GetReadyNodes(ctx, redisCluster)
	if redisClients[nodeId] == nil {
		secret, _ := r.GetRedisSecret(redisCluster)
		rdb := r.GetRedisClient(ctx, nodes[nodeId].IP, secret)
		redisClients[nodeId] = &RedisClient{NodeId: nodeId, RedisClient: rdb}
	}

	return redisClients[nodeId].RedisClient
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
		return err
	}
	r.Recorder.Event(redisCluster, "Normal", "ClusterMeet", "Redis cluster meet completed.")

	r.AssignSlots(ctx, readyNodes, redisCluster)
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

func (r *RedisClusterReconciler) ScaleCluster(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {
	var err error
	sset_err, sset := r.FindExistingStatefulSet(ctx, controllerruntime.Request{NamespacedName: types.NamespacedName{Name: redisCluster.Name, Namespace: redisCluster.Namespace}})
	if sset_err != nil {
		return err
	}
	readyNodes, _ := r.GetReadyNodes(ctx, redisCluster)
	currSsetReplicas := *(sset.Spec.Replicas)
	redisCluster.Status.Slots = r.GetSlotsRanges(redisCluster.Spec.Replicas)
	if redisCluster.Spec.Replicas < currSsetReplicas {
		// downscaling, migrate nodes
		r.Log.Info("redisCluster.Spec.Replicas < statefulset.Spec.Replicas, downscaling", "rc.s.r", redisCluster.Spec.Replicas, "sset.s.r", currSsetReplicas)
		err = r.RebalanceCluster(ctx, redisCluster)
		if err != nil {
			r.Log.Error(err, "Issues with rebalancing cluster when scaling down")
			return err
		}
		r.ForgetUnnecessaryNodes(ctx, redisCluster)
	}

	if int(redisCluster.Spec.Replicas) == len(readyNodes) {
		r.ClusterMeet(ctx, readyNodes, redisCluster)
		time.Sleep(5 * time.Second)
		err = r.RebalanceCluster(ctx, redisCluster)
		if err != nil {
			r.Log.Error(err, "ScaleCluster - issue with rebalancing cluster when scaling up")
			return err
		}
	}

	newSize := redisCluster.Spec.Replicas
	sset.Spec.Replicas = &newSize
	r.Log.Info("ScaleCluster - updating statefulset replicas", "newsize", newSize)
	r.Client.Update(ctx, sset)
	return nil
}

func (r *RedisClusterReconciler) MoveSlot(ctx context.Context, slot int, src_node, dst_node *v1alpha1.RedisNode, redisCluster *v1alpha1.RedisCluster) error {
	if dst_node == nil || src_node == nil {
		return fmt.Errorf("dst or src node does not exist")
	}
	dstClient := r.GetRedisClientForNode(ctx, dst_node.NodeID, redisCluster)
	srcClient := r.GetRedisClientForNode(ctx, src_node.NodeID, redisCluster)
	var err error

	err = dstClient.Do(ctx, "cluster", "setslot", slot, "importing", src_node.NodeID).Err()
	if err != nil {
		return err
	}

	err = srcClient.Do(ctx, "cluster", "setslot", slot, "migrating", dst_node.NodeID).Err()
	if err != nil {
		return err
	}

	// todo: batching
	for i := 1; ; i++ {
		keysInSlot := srcClient.ClusterGetKeysInSlot(ctx, slot, 100).Val()
		if len(keysInSlot) == 0 {
			break
		} else {
			if slot%100 == 0 {
				r.Log.Info("MoveSlot - found keys in slot", "slot", slot, "count", len(keysInSlot))
			}
		}
		for _, key := range keysInSlot {
			// todo: batch migrate
			err = srcClient.Migrate(ctx, dst_node.IP, strconv.Itoa(redis.RedisCommPort), key, 0, 1*time.Second).Err()
			if err != nil {
				r.Log.Error(err, "Migrate failed", "key", key, "keysinslot", keysInSlot)
				return err
			}
		}
	}
	err = srcClient.Do(ctx, "cluster", "setslot", slot, "node", dst_node.NodeID).Err()
	if err != nil {
		r.Log.Error(err, "Setslot failed", "slot", slot, "node", dst_node)
		return err
	}
	err = dstClient.Do(ctx, "cluster", "setslot", slot, "node", dst_node.NodeID).Err()
	if err != nil {
		r.Log.Error(err, "Setslot failed", "slot", slot, "node", dst_node)
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
	var alphaNode *v1alpha1.RedisNode
	for nodeId, node := range nodes {
		r.Log.Info("ClusterMeet", "node", node)
		if alphaNode == nil {
			alphaNode = node
			rdb = r.GetRedisClientForNode(ctx, alphaNode.NodeID, redisCluster)
			r.Log.Info("ClusterMeet", "alphaNode", alphaNode, "rdb", rdb)
		}
		r.Log.Info("Running cluster meet", "srcnode", alphaNode.NodeID, "dstnode", nodeId)
		err := rdb.ClusterMeet(ctx, node.IP, strconv.Itoa(redis.RedisCommPort)).Err()
		if err != nil {
			r.Log.Error(err, "clustermeet failed", "nodes", node)
			return err
		}
	}
	return nil
}

func (r *RedisClusterReconciler) GetSlotsRanges(nodes int32) []*v1alpha1.SlotRange {
	slots := redis.SplitNodeSlots(int(nodes))
	var apiRedisSlots []*v1alpha1.SlotRange = make([]*v1alpha1.SlotRange, 0)
	for _, node := range slots {
		apiRedisSlots = append(apiRedisSlots, &v1alpha1.SlotRange{Start: node.Start, End: node.End})
	}
	r.Log.Info("GetSlotsRanges", "slots", slots, "ranges", apiRedisSlots)
	return apiRedisSlots
}

func (r *RedisClusterReconciler) GetAnyRedisClient(ctx context.Context, redisCluster *v1alpha1.RedisCluster) *redisclient.Client {
	nodes, _ := r.GetReadyNodes(ctx, redisCluster)
	var client *redisclient.Client

	for _, n := range nodes {
		client = r.GetRedisClientForNode(ctx, n.NodeID, redisCluster)
		break
	}
	return client
}

func (r *RedisClusterReconciler) GetClusterSlotConfiguration(ctx context.Context, redisCluster *v1alpha1.RedisCluster) []redisclient.ClusterSlot {
	client := r.GetAnyRedisClient(ctx, redisCluster)
	clusterSlots := client.ClusterSlots(ctx).Val()
	// todo: error handling
	return clusterSlots
}

func (r *RedisClusterReconciler) NodeBySequence(nodes map[string]*v1alpha1.RedisNode) ([]*v1alpha1.RedisNode, error) {
	nodesBySequence := make([]*v1alpha1.RedisNode, len(nodes))
	for _, node := range nodes {
		nodeNameElements := strings.Split(node.NodeName, "-")
		nodePodSequence, err := strconv.Atoi(nodeNameElements[len(nodeNameElements)-1])
		if err != nil {
			return nil, err
		}
		nodesBySequence[nodePodSequence] = node
	}
	return nodesBySequence, nil
}

func (r *RedisClusterReconciler) ForgetUnnecessaryNodes(ctx context.Context, redisCluster *v1alpha1.RedisCluster) {
	readyNodes, _ := r.GetReadyNodes(ctx, redisCluster)
	remainingNodes := make([]*v1alpha1.RedisNode, 0)
	overstayedTheirWelcomeNodes := make([]*v1alpha1.RedisNode, 0)
	nodesBySequence, _ := r.NodeBySequence(readyNodes)
	for seq, node := range nodesBySequence {
		if seq < len(redisCluster.Status.Slots) {
			remainingNodes = append(remainingNodes, node)
		} else {
			overstayedTheirWelcomeNodes = append(overstayedTheirWelcomeNodes, node)
		}
	}
	for _, remainingNode := range remainingNodes {
		for _, forgottenNode := range overstayedTheirWelcomeNodes {
			client := r.GetRedisClientForNode(ctx, remainingNode.NodeID, redisCluster)
			err := client.Do(ctx, "cluster", "forget", forgottenNode.NodeID).Err()
			if err != nil {
				r.Log.Error(err, "Node forget failed", "target", remainingNode, "forgottenNode", forgottenNode)
			}

		}
	}
	for _, node := range overstayedTheirWelcomeNodes {
		r.RemoveRedisClientForNode(node.NodeID, ctx, redisCluster)
	}
}

func (r *RedisClusterReconciler) RebalanceCluster(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {
	var err error
	// get current slots assignment
	clusterSlots := r.GetClusterSlotConfiguration(ctx, redisCluster)
	// get slots map source from the cluster status field
	slotsMap := redisCluster.Status.Slots

	// get ready nodes
	readyNodes, _ := r.GetReadyNodes(ctx, redisCluster)
	// ensure there are enough ready nodes for allocation
	if len(readyNodes) < len(slotsMap) {
		return fmt.Errorf("Got %d readyNodes, but need %d readyNodes to satisfy slots map allocation.", len(readyNodes), len(slotsMap))
	}
	r.Log.Info("RebalanceCluster", "nodes", readyNodes, "slotsMap", slotsMap)

	// iterate over slots map
	for _, slotRange := range clusterSlots {
		for slot := slotRange.Start; slot <= slotRange.End; slot++ {
			dstNodeId, err := r.GetSlotOwnerCandidate(slot, redisCluster)
			if err != nil {
				return err
			}
			srcNodeId := slotRange.Nodes[0].ID
			if srcNodeId == dstNodeId {
				continue
			}
			srcNode := readyNodes[srcNodeId]
			dstNode := readyNodes[dstNodeId]
			if srcNode == nil {
				return fmt.Errorf("srcNode with nodeid %s not found in the list of nodes", srcNodeId)
			}
			if srcNode == nil {
				return fmt.Errorf("srcNode with nodeid %s not found in the list of nodes", dstNodeId)
			}

			err = r.MoveSlot(ctx, slot, srcNode, dstNode, redisCluster)
		}
	}

	return err
}

func (r *RedisClusterReconciler) GetSlotOwnerCandidate(slot int, redisCluster *v1alpha1.RedisCluster) (string, error) {
	readyNodes, _ := r.GetReadyNodes(context.TODO(), redisCluster)
	if len(readyNodes) < len(redisCluster.Status.Slots) {
		return "", fmt.Errorf("Not enough readyNodes to satisfy slots map: nodes=%d, ranges=%d", len(readyNodes), len(redisCluster.Status.Slots))
	}
	nodesBySequence, _ := r.NodeBySequence(readyNodes)

	slotsMap := redisCluster.Status.Slots
	for k, slotRange := range slotsMap {
		if slot <= slotRange.End && slot >= slotRange.Start {
			if nodesBySequence[k] == nil {
				return "", fmt.Errorf("Expected slot to be in a node sequence %d, however no such pod exists", k)
			}
			return nodesBySequence[k].NodeID, nil
		}
	}
	return "", nil
}

//TODO: check how many cluster slots have been already assign, and rebalance cluster if necessary
func (r *RedisClusterReconciler) AssignSlots(ctx context.Context, nodes map[string]*v1alpha1.RedisNode, redisCluster *v1alpha1.RedisCluster) {
	// when all nodes are formed in a cluster, addslots
	r.Log.Info("AssignSlots", "nodeslen", len(nodes), "nodes", nodes)
	slots := redis.SplitNodeSlots(len(nodes))
	i := 0
	for _, node := range nodes {
		rdb := r.GetRedisClientForNode(ctx, node.NodeID, redisCluster)
		rdb.ClusterAddSlotsRange(ctx, slots[i].Start, slots[i].End)
		r.Log.Info("Running cluster assign slots", "pods", nodes)
		i++
	}
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

func (r *RedisClusterReconciler) GetRedisClusterPods(ctx context.Context, clusterName string) *corev1.PodList {
	allPods := &corev1.PodList{}
	labelSelector := labels.SelectorFromSet(
		map[string]string{
			redis.RedisClusterLabel: clusterName,
			"app":                   "redis",
		},
	)

	r.Client.List(ctx, allPods, &client.ListOptions{
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
				nodeId := redisClient.Do(ctx, "cluster", "myid").Val()
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
