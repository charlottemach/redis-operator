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

func (r *RedisClusterReconciler) StatefulSetChanges(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {
	var err error

	redisSecret, err := r.GetRedisSecret(redisCluster)
	if err != nil {
		r.Log.Info("ConfigureRedisCluster - secret not found", "name", redisCluster.GetName())
		return err
	}
	readyNodes, _ := r.GetReadyNodes(ctx, redisCluster)
	if !reflect.DeepEqual(readyNodes, redisCluster.Status.Nodes) {
		r.ClusterMeet(ctx, readyNodes, redisSecret)
		r.Recorder.Event(redisCluster, "Normal", "ClusterMeet", "Redis cluster meet completed.")
	}

	if len(readyNodes) == int(redisCluster.Spec.Replicas) {
		r.AssignSlots(ctx, readyNodes, redisSecret)
		r.Recorder.Event(redisCluster, "Normal", "SlotAssignment", "Slot assignment execution complete")
	}

	return nil
}

func (r *RedisClusterReconciler) RedisClusterChanges(ctx context.Context, redisCluster *v1alpha1.RedisCluster) error {
	var err error

	if err != nil {
		r.Log.Info("ConfigureRedisCluster - secret not found", "name", redisCluster.GetName())
		return err
	}
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
				// redisCluster.Status.Status = v1alpha1.StatusScalingDown
				// this pod gets removed, but first migrate all slots, once this done, update sset replicas count
				// todo migrate slot
				err := r.MigrateSlots(ctx, node, redisCluster)
				if err != nil {
					r.Log.Error(err, "MigrateSlots")
					return err
				}
				newSize := currSsetReplicas - 1
				sset.Spec.Replicas = &newSize
				r.Log.Info("RedisClusterUpdate - updating sset count", "newsize", newSize)
				r.Client.Update(ctx, sset)
				break
			}
		}
	}

	// if redisCluster.Spec.Replicas == currSsetReplicas {
	// 	if redisCluster.Status.Status == v1alpha1.StatusScalingDown {
	// 		redisCluster.Status.Status = v1alpha1.StatusReady
	// 	}
	// }

	return nil
}

func (r *RedisClusterReconciler) MigrateSlots(ctx context.Context, src_node *v1alpha1.RedisNode, redisCluster *v1alpha1.RedisCluster) error {
	// get current slot range served by the node
	// for each slot, set status importing / migrating
	// in round robin fashion, migrate the slot to another cluster node
	// note: this operation should be able to resume
	secret, _ := r.GetRedisSecret(redisCluster)
	srcClient := r.GetRedisClient(ctx, src_node.IP, secret)
	nodes, _ := r.GetReadyNodes(ctx, redisCluster)
	defer srcClient.Close()

	// get all destination nodes
	nodeIds := make([]string, 0)
	for nodeId := range nodes {
		if nodeId != src_node.NodeID {
			nodeIds = append(nodeIds, nodeId)
		}
	}
	r.Log.Info("MigrateSlots", "src", src_node.NodeName)
	slots := srcClient.ClusterSlots(ctx).Val()

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
			dstClient := r.GetRedisClient(ctx, nodes[destNodeId].IP, secret)

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
			dstClient.Close()
		}
	}
	someNodeId := nodeIds[rand.Intn(len(nodeIds)-1)]
	someClient := r.GetRedisClient(ctx, nodes[someNodeId].IP, secret)
	defer someClient.Close()
	err := someClient.Do(ctx, "cluster", "forget", src_node.NodeID).Err()
	if err != nil {
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

func (r *RedisClusterReconciler) ClusterMeet(ctx context.Context, nodes map[string]*v1alpha1.RedisNode, secret string) {
	r.Log.Info("ClusterMeet", "nodes", nodes)
	var rdb *redisclient.Client
	if len(nodes) == 0 {
		return
	}

	var node *v1alpha1.RedisNode

	for _, v := range nodes {
		if node == nil {
			node = v
			rdb = r.GetRedisClient(ctx, node.IP, secret)
			defer rdb.Close()
		}
		r.Log.Info("Running cluster meet", "node", node)
		err := rdb.ClusterMeet(ctx, v.IP, strconv.Itoa(redis.RedisCommPort)).Err()
		if err != nil {
			r.Log.Error(err, "clustermeet failed", "nodes", nodes)
		}
	}
}

//TODO: check how many cluster slots have been already assign, and rebalance cluster if necessary
func (r *RedisClusterReconciler) AssignSlots(ctx context.Context, nodes map[string]*v1alpha1.RedisNode, secret string) {
	// when all nodes are formed in a cluster, addslots
	r.Log.Info("ClusterMeet", "nodes", nodes)
	slots := redis.SplitNodeSlots(len(nodes))
	i := 0
	for _, node := range nodes {
		rdb := r.GetRedisClient(ctx, node.IP, secret)
		defer rdb.Close()
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
		r.Log.Info("All pods list", "pod", pod.GetName(), "labels", pod.Labels)
		for _, s := range pod.Status.Conditions {
			if s.Type == corev1.PodReady && s.Status == corev1.ConditionTrue {
				r.Log.Info("Pod status ready", "podname", pod.Name, "conditions", pod.Status.Conditions)
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
		r.Log.Info("GetRedisSecret - no secret name is set in the cluster, exiting")
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
