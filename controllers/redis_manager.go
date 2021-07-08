package controllers

import (
	"errors"
	"math/rand"
	"reflect"
	"strconv"
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

func (r *RedisClusterReconciler) GetRedisClientForNode(ctx context.Context, nodeId string, redisCluster *v1alpha1.RedisCluster) *redisclient.Client {
	nodes, _ := r.GetReadyNodes(ctx, redisCluster)
	if redisClients[nodeId] == nil {
		secret, _ := r.GetRedisSecret(redisCluster)
		rdb := r.GetRedisClient(ctx, nodes[nodeId].IP, secret)
		redisClients[nodeId] = &RedisClient{NodeId: nodeId, RedisClient: rdb}
	}

	return redisClients[nodeId].RedisClient
}

func (r *RedisClusterReconciler) ConfigureRedisCluster(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {
	readyNodes, _ := r.GetReadyNodes(ctx, redisCluster)
	r.Log.Info("ConfigureRedisCluster", "readyNodes", readyNodes, "equality", reflect.DeepEqual(readyNodes, redisCluster.Status.Nodes))
	r.ClusterMeet(ctx, readyNodes, redisCluster)
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
			r.Recorder.Event(redisCluster, "Normal", "ClusterReady", "Redis cluster scaling down complete.")
			redisCluster.Status.Status = v1alpha1.StatusReady
		}
		if redisCluster.Status.Status == v1alpha1.StatusScalingUp {
			r.Recorder.Event(redisCluster, "Normal", "ClusterScalingUp", "Redis cluster scaling up complete.")
			if len(redisCluster.Status.Nodes) == int(currSsetReplicas) {
				redisCluster.Status.Status = v1alpha1.StatusReady
			}
		}
	}

	return nil
}

func (r *RedisClusterReconciler) ScaleCluster(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {
	var err error
	// todo: process error
	sset_err, sset := r.FindExistingStatefulSet(ctx, controllerruntime.Request{NamespacedName: types.NamespacedName{Name: redisCluster.Name, Namespace: redisCluster.Namespace}})
	if sset_err != nil {
		return err
	}
	currSsetReplicas := *(sset.Spec.Replicas)
	if redisCluster.Spec.Replicas < currSsetReplicas {
		// downscaling, migrate nodes
		r.Log.Info("redisCluster.Spec.Replicas < sset.Spec.Replicas, downscaling", "rc.s.r", redisCluster.Spec.Replicas, "sset.s.r", currSsetReplicas)

		// get the last replica of the sset
		redisNodes, _ := r.GetReadyNodes(ctx, redisCluster)
		// update sset replicas to current - 1

		for _, node := range redisNodes {
			r.Log.Info("Checking pod name", "podname", node.NodeName, "generated name", fmt.Sprintf("%s-%d", redisCluster.Name, currSsetReplicas-1))
			if node.NodeName == fmt.Sprintf("%s-%d", redisCluster.Name, currSsetReplicas-1) {
				r.Log.Info("RedisClusterUpdate, found last pod", "podName", node.NodeName)
				// this pod gets removed, but first migrate all slots, once this done, update sset replicas count
				err := r.MigrateSlots(ctx, node, redisCluster)
				if err != nil {
					r.Log.Error(err, "MigrateSlots")
					return err
				}
				newSize := currSsetReplicas - 1
				sset.Spec.Replicas = &newSize
				r.Log.Info("RedisClusterUpdate - updating sset count", "newsize", newSize)
				r.Client.Update(ctx, sset)

				// pvc := &corev1.PersistentVolumeClaim{}
				// pvcName := "data-" + redisCluster.Name + fmt.Sprintf("-%d", (currSsetReplicas-1))
				// r.Client.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: redisCluster.Namespace}, pvc)
				// r.Log.Info("ScaleCluster - Deleting pvc", "pvc", pvc)
				// r.Client.Delete(ctx, pvc)

				break
			}
		}
	}

	if redisCluster.Spec.Replicas > currSsetReplicas {
		// change status to
		redisCluster.Status.Status = v1alpha1.StatusScalingUp
		newSize := currSsetReplicas + 1
		sset.Spec.Replicas = &newSize
		r.Log.Info("RedisClusterUpdate - updating sset count", "newsize", newSize)
		r.Client.Update(ctx, sset)
	}

	if redisCluster.Spec.Replicas == currSsetReplicas {
		readyNodes := redisCluster.Status.Nodes
		dstNodeId := ""
		if int32(len(readyNodes)) == currSsetReplicas {
			for nodeId, node := range readyNodes {
				r.Log.Info("ScaleCluster - looking for dstNodeId", "candidate", fmt.Sprintf("%s-%d", redisCluster.Name, currSsetReplicas-1), "nodeName", node.NodeName)
				if node.NodeName == fmt.Sprintf("%s-%d", redisCluster.Name, currSsetReplicas-1) {
					dstNodeId = nodeId
				}
			}
			if dstNodeId == "" {
				r.Log.Error(nil, "dstNode couldn't be assigned", "name", redisCluster.Name, "index", currSsetReplicas-1)
				return err
			}
			r.Log.Info("ScaleCluster - all nodes are ready. Scaling up", "replicas", currSsetReplicas)
			r.ClusterMeet(ctx, readyNodes, redisCluster)
			time.Sleep(time.Second * 5)
			err := r.PopulateSlots(ctx, readyNodes[dstNodeId], redisCluster)
			if err != nil {
				r.Log.Error(err, "populateslots failed")
				return err
			}
		}
	}
	return nil
}

/*
   New methods stubs:
   ScaleDown
   ScaleUp
   ScaleInProgress
   ForgetNode
*/

func (r *RedisClusterReconciler) PopulateSlots(ctx context.Context, dst_node *v1alpha1.RedisNode, redisCluster *v1alpha1.RedisCluster) error {
	dstClient := r.GetRedisClientForNode(ctx, dst_node.NodeID, redisCluster)
	readyNodes, _ := r.GetReadyNodes(ctx, redisCluster)

	// get all destination nodes
	donorNodeIds := make([]string, 0)
	for nodeId := range readyNodes {
		if nodeId != dst_node.NodeID {
			donorNodeIds = append(donorNodeIds, nodeId)
		}
	}
	slotsMigrated := make(map[string]int)
	r.Log.Info("MigrateSlots", "dst", dst_node.NodeName)
	slots := r.GetRedisClientForNode(ctx, donorNodeIds[0], redisCluster).ClusterSlots(ctx).Val()
	//
	slotsToMigrateFromOneDonor := 16384 / len(readyNodes) / len(donorNodeIds)
	for _, v := range slots {
		for slot := v.Start; slot <= v.End; slot++ {
			if slot == v.Start {
				r.Log.Info("MigrateSlots - migration slot start", "slot", v)
			}
			srcNodeId := v.Nodes[0].ID

			if slotsMigrated[srcNodeId] >= slotsToMigrateFromOneDonor {
				r.Log.Info("PopulateSlots", "slotstomigratefromdonor", slotsMigrated[srcNodeId], "donor", srcNodeId, "stats", slotsMigrated)
				continue
			}

			srcClient := r.GetRedisClientForNode(ctx, srcNodeId, redisCluster)

			err := dstClient.Do(ctx, "cluster", "setslot", slot, "importing", srcNodeId).Err()
			if err != nil {
				return err
			}

			err = srcClient.Do(ctx, "cluster", "setslot", slot, "migrating", dst_node.NodeID).Err()
			if err != nil {
				return err
			}

			// todo: batching
			for i := 1; ; i++ {
				keysInSlot := srcClient.ClusterGetKeysInSlot(ctx, slot, 1000).Val()
				if len(keysInSlot) == 0 {
					break
				}
				for _, key := range keysInSlot {
					err = dstClient.Migrate(ctx, readyNodes[dst_node.NodeID].IP, strconv.Itoa(redis.RedisCommPort), key, 0, 30*time.Second).Err()
					if err != nil {
						return err
					}
				}
			}
			err = srcClient.Do(ctx, "cluster", "setslot", slot, "node", dst_node.NodeID).Err()
			if err != nil {
				return err
			}
			err = dstClient.Do(ctx, "cluster", "setslot", slot, "node", dst_node.NodeID).Err()
			if err != nil {
				return err
			}
			slotsMigrated[srcNodeId]++
		}
	}
	return nil
}

func (r *RedisClusterReconciler) MigrateSlots(ctx context.Context, src_node *v1alpha1.RedisNode, redisCluster *v1alpha1.RedisCluster) error {
	// get current slot range served by the node
	// for each slot, set status importing / migrating
	// in round robin fashion, migrate the slot to another cluster node
	// note: this operation should be able to resume
	srcClient := r.GetRedisClientForNode(ctx, src_node.NodeID, redisCluster)
	nodes, _ := r.GetReadyNodes(ctx, redisCluster)

	// get all destination nodes
	nodeIds := make([]string, 0)
	for nodeId := range nodes {
		if nodeId != src_node.NodeID {
			nodeIds = append(nodeIds, nodeId)
		}
	}
	r.Log.Info("MigrateSlots", "src", src_node.NodeName)
	slots := srcClient.ClusterSlots(ctx).Val()
	slotsMigrated := make(map[string]int)
	for _, v := range slots {
		if len(v.Nodes) > 0 {
			if v.Nodes[0].ID != src_node.NodeID {
				r.Log.Info("MigrateSlots - src not owner of slot", "slot", v)
				continue
			}
		}
		r.Log.Info("MigrateSlots", "slot", v)
		for slot := v.Start; slot <= v.End; slot++ {
			if slot == v.Start {
				r.Log.Info("MigrateSlots - migration slot start", "slot", v)
			}
			destNodeId := nodeIds[rand.Intn(len(nodeIds))]
			dstClient := r.GetRedisClientForNode(ctx, destNodeId, redisCluster)

			err := dstClient.Do(ctx, "cluster", "setslot", slot, "importing", src_node.NodeID).Err()
			if err != nil {
				return err
			}

			err = srcClient.Do(ctx, "cluster", "setslot", slot, "migrating", destNodeId).Err()
			if err != nil {
				return err
			}

			// todo: batching
			for i := 1; ; i++ {
				keysInSlot := srcClient.ClusterGetKeysInSlot(ctx, slot, 1000).Val()
				if len(keysInSlot) == 0 {
					break
				}
				for _, key := range keysInSlot {
					err = dstClient.Migrate(ctx, nodes[destNodeId].IP, strconv.Itoa(redis.RedisCommPort), key, 0, 30*time.Second).Err()
					if err != nil {
						return err
					}
				}
			}
			err = srcClient.Do(ctx, "cluster", "setslot", slot, "node", destNodeId).Err()
			if err != nil {
				return err
			}
			err = dstClient.Do(ctx, "cluster", "setslot", slot, "node", destNodeId).Err()
			if err != nil {
				return err
			}
			slotsMigrated[destNodeId]++
		}
	}
	r.Log.Info("MigrateSlots - complete", "srcnode", src_node.NodeID, "migrationStats", slotsMigrated)
	for _, nodeId := range nodeIds {
		client := r.GetRedisClientForNode(ctx, nodeId, redisCluster)
		r.Log.Info("MigrateSlots - running cluster forget on node", "node", nodeId, "client", client)
		err := client.Do(ctx, "cluster", "forget", src_node.NodeID).Err()
		if err != nil {
			r.Log.Error(err, "Node forget failed")
		}

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

func (r *RedisClusterReconciler) ClusterMeet(ctx context.Context, nodes map[string]*v1alpha1.RedisNode, redisCluster *v1alpha1.RedisCluster) {
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
		}
	}
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
