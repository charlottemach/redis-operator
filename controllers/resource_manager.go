package controllers

import (
	"reflect"
	"strconv"

	//"golang.org/x/tools/godoc/redirect"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"

	"context"
	"fmt"

	redisclient "github.com/go-redis/redis/v8"

	//"golang.org/x/tools/godoc/redirect"

	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	// "k8s.io/apiextensions-apiserver/pkg/client/clientset"

	"k8s.io/apimachinery/pkg/types"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/containersolutions/redis-operator/api/v1alpha1"
	redis "github.com/containersolutions/redis-operator/internal/redis"
	"k8s.io/apimachinery/pkg/labels"
)

func (r *RedisClusterReconciler) ConfigureRedisCluster(ctx context.Context, o client.Object) error {
	r.Log.Info("ConfigureRedisCluster", "o", r.GetObjectKey(o))
	var err error
	redisCluster := &v1alpha1.RedisCluster{}
	cluster_find_error := r.FindInternalResource(ctx, o, redisCluster)
	if cluster_find_error != nil {
		r.Log.Info("ConfigureRedisCluster - error", "name", redisCluster.GetName())
		return cluster_find_error
	}
	redisSecret, err := r.GetRedisSecret(o)
	if err != nil {
		r.Log.Info("ConfigureRedisCluster - secret not found", "name", redisCluster.GetName())
		return err
	}
	readyNodes := r.GetReadyNodes(ctx, redisCluster.GetName())
	if !reflect.DeepEqual(readyNodes, redisCluster.Status.Nodes) {
		redisCluster.Status.Nodes = readyNodes
		r.ClusterMeet(ctx, readyNodes, redisSecret)
		r.Recorder.Event(redisCluster, "Normal", "ClusterMeet", "Redis cluster meet completed.")
	}

	if len(readyNodes) == int(redisCluster.Spec.Replicas) {
		r.AssignSlots(ctx, readyNodes, redisSecret)
		r.Recorder.Event(redisCluster, "Normal", "SlotAssignment", "Slot assignment execution complete")
	}

	r.Log.Info("Checking if cluster status can be updated to Ready")
	// check the cluster state and slots allocated. if states is ok, we can reset the status
	clusterInfo := r.GetClusterInfo(ctx, redisCluster)
	state := clusterInfo["cluster_state"]
	slots_ok := clusterInfo["cluster_slots_ok"]

	if state == "ok" && slots_ok == "16384" {
		r.Log.Info("Cluster state to Ready")
		redisCluster.Status.Status = v1alpha1.StatusReady
	} else {
		r.Log.Info("Cluster state to Configuring")
		redisCluster.Status.Status = v1alpha1.StatusConfiguring
	}

	return r.UpdateClusterStatus(ctx, redisCluster)
}

func (r *RedisClusterReconciler) isOwnedByUs(o client.Object) bool {
	labels := o.GetLabels()
	if _, found := labels[redis.RedisClusterLabel]; found {
		return true
	}
	return false
}

func (r *RedisClusterReconciler) ClusterMeet(ctx context.Context, nodes []v1alpha1.RedisNode, secret string) {
	r.Log.Info("ClusterMeet", "nodes", nodes)
	if len(nodes) == 0 {
		return
	}
	node := nodes[0]
	rdb := r.GetRedisClient(ctx, node.IP, secret)
	for _, v := range nodes[1:] {
		r.Log.Info("Running cluster meet", "node", v, "endpoint", node.IP)
		err := rdb.ClusterMeet(ctx, v.IP, strconv.Itoa(redis.RedisCommPort)).Err()
		if err != nil {
			r.Log.Error(err, "clustermeet failed", "nodes", nodes[1:])
		}
	}
}

//TODO: check how many cluster slots have been already assign, and rebalance cluster if necessary
func (r *RedisClusterReconciler) AssignSlots(ctx context.Context, nodes []v1alpha1.RedisNode, secret string) {
	// when all nodes are formed in a cluster, addslots
	r.Log.Info("ClusterMeet", "nodes", nodes)
	slots := redis.SplitNodeSlots(len(nodes))
	i := 0
	for _, node := range nodes {
		rdb := r.GetRedisClient(ctx, node.IP, secret)
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

func (r *RedisClusterReconciler) GetReadyNodes(ctx context.Context, clusterName string) []v1alpha1.RedisNode {
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
	readyNodes := make([]v1alpha1.RedisNode, 0)
	for _, pod := range allPods.Items {
		r.Log.Info("All pods list", "pod", pod.GetName(), "labels", pod.Labels)
		for _, s := range pod.Status.Conditions {
			if s.Type == corev1.PodReady && s.Status == corev1.ConditionTrue {
				r.Log.Info("Pod status ready", "podname", pod.Name, "conditions", pod.Status.Conditions)
				readyNodes = append(readyNodes, v1alpha1.RedisNode{IP: pod.Status.PodIP, NodeName: pod.GetName()})
			}
		}
	}

	return readyNodes
}

func (r *RedisClusterReconciler) ReapplyConfiguration(ctx context.Context, o client.Object) error {
	into := &v1.StatefulSet{}
	err := r.FindInternalResource(ctx, o, into)
	if err != nil {
		r.Log.Error(err, "Can't find internal resource - StatefulSet")
		return err
	}
	redisCluster := &v1alpha1.RedisCluster{}
	err = r.FindInternalResource(ctx, o, redisCluster)
	if err != nil {
		r.Log.Error(err, "Can't find internal resource - RedisCluster")
		return err
	}
	readyNodes := r.GetReadyNodes(ctx, redisCluster.GetName())
	secret, _ := r.GetRedisSecret(o)
	r.Log.Info("Secret and ready nodes", "readyNodes", readyNodes)
	i := 0
	for _, node := range readyNodes {
		rdb := r.GetRedisClient(ctx, node.IP, secret)
		r.Log.Info("Running redis node shutdown", "node", node)
		rdb.Shutdown(ctx)
		r.Log.Info("Restarting Redis nodes", "pods", readyNodes)
		i++
	}

	r.Recorder.Event(redisCluster, "Normal", "Configuration", "Configuration re-apply.")
	return nil
}

func (r *RedisClusterReconciler) GetRedisSecret(o client.Object) (string, error) {
	r.Log.Info("GetRedisSecret", "o", r.GetObjectKey(o))
	redisCluster := &v1alpha1.RedisCluster{}
	err := r.FindInternalResource(context.TODO(), o, redisCluster)
	if err != nil {
		return "", err
	}

	if redisCluster.Spec.Auth.SecretName == "" {
		r.Log.Info("GetRedisSecret - no secret name is set in the cluster, exiting")
		return "", nil
	}

	secret := &corev1.Secret{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: redisCluster.Spec.Auth.SecretName, Namespace: o.GetNamespace()}, secret)
	if err != nil {
		return "", err
	}
	redisSecret := string(secret.Data["requirepass"])
	return redisSecret, nil
}
