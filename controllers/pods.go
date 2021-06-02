package controllers

import (
	"strconv"

	//"golang.org/x/tools/godoc/redirect"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"
	"reflect"

	"context"
	"fmt"

	redisclient "github.com/go-redis/redis/v8"

	//"golang.org/x/tools/godoc/redirect"

	corev1 "k8s.io/api/core/v1"

	// "k8s.io/apiextensions-apiserver/pkg/client/clientset"

	"k8s.io/apimachinery/pkg/types"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/containersolutions/redis-operator/api/v1alpha1"
	redis "github.com/containersolutions/redis-operator/internal/redis"
	"k8s.io/apimachinery/pkg/labels"
)

func (r *RedisClusterReconciler) ReconcilePod(ctx context.Context, req ctrl.Request, pod *corev1.Pod) (ctrl.Result, error) {
	nsNameCluster := types.NamespacedName{
		Name:      pod.GetLabels()["rediscluster"],
		Namespace: req.Namespace,
	}
	err, redisCluster := r.FindRedisCluster(ctx, nsNameCluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	redisSecret := ""
	if redisCluster.Spec.Auth.SecretName != "" {
		secret := &corev1.Secret{}
		err := r.Client.Get(ctx, types.NamespacedName{Name: redisCluster.Spec.Auth.SecretName, Namespace: req.Namespace}, secret)
		if err != nil {
			r.Log.Error(err, "Can't find secret for cluster", "auth", redisCluster.Spec.Auth)
			return ctrl.Result{}, err
		}
		redisSecret = string(secret.Data["requirepass"])

	}

	readyNodes := r.GetReadyNodes(ctx, redisCluster.GetName())
	if !reflect.DeepEqual(readyNodes, redisCluster.Status.Nodes) {
		redisCluster.Status.Nodes = readyNodes
		err = r.Status().Update(ctx, redisCluster)
		if err != nil {
			r.Log.Error(err, "Failed to update rediscluster status")
			return ctrl.Result{}, err
		}
		r.ClusterMeet(ctx, readyNodes, redisSecret)
		r.Recorder.Event(redisCluster, "Normal", "ClusterMeet", "Redis cluster meet completed.")
	}
	r.Log.Info("cluster", "clustersstate", redisCluster.Status)

	if len(readyNodes) == int(redisCluster.Spec.Replicas) {
		r.AssignSlots(ctx, readyNodes, redisSecret)
		r.Recorder.Event(redisCluster, "Normal", "SlotAssignment", "Slot assignment execution complete")
	}

	return ctrl.Result{}, nil
}

func (r *RedisClusterReconciler) isOwnedByUs(o client.Object) bool {

	labels := o.GetLabels()

	if _, found := labels["rediscluster"]; found {
		return true
	}
	return false
}

func (r *RedisClusterReconciler) ClusterMeet(ctx context.Context, nodes []v1alpha1.RedisNode, secret string) {
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

func (r *RedisClusterReconciler) AssignSlots(ctx context.Context, nodes []v1alpha1.RedisNode, secret string) {
	// when all nodes are formed in a cluster, addslots
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

func (r *RedisClusterReconciler) FindRedisCluster(ctx context.Context, ns types.NamespacedName) (error, *v1alpha1.RedisCluster) {
	redisCluster := &v1alpha1.RedisCluster{}
	err := r.Client.Get(ctx, ns, redisCluster)
	return err, redisCluster
}

func (r *RedisClusterReconciler) GetReadyNodes(ctx context.Context, clusterName string) []v1alpha1.RedisNode {
	allPods := &corev1.PodList{}
	labelSelector := labels.SelectorFromSet(
		map[string]string{
			"rediscluster": clusterName,
			"app":          "redis",
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
