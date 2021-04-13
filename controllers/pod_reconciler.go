package controllers

import (
	"context"
	"fmt"
	"strconv"

	//"golang.org/x/tools/godoc/redirect"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"
	"github.com/containersolutions/redis-operator/api/v1alpha1"
	"github.com/containersolutions/redis-operator/internal/redis"
	"github.com/go-logr/logr"
	redisclient "github.com/go-redis/redis/v8"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type RedisMeet struct {
	PodsNames map[string]string
}

var meet = make(map[string]*RedisMeet)

type PodReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func (r *PodReconciler) FindRedisCluster(ctx context.Context, ns types.NamespacedName) (error, *v1alpha1.RedisCluster) {
	redisCluster := &v1alpha1.RedisCluster{}
	err := r.Client.Get(ctx, ns, redisCluster)
	return err, redisCluster
}

func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("rediscluster - pod reconciler", req.NamespacedName)
	r.Log.Info("Pod reconciler", "ns", req.NamespacedName)
	pod := &corev1.Pod{}
	err := r.Client.Get(ctx, req.NamespacedName, pod)

	if err != nil {
		if !errors.IsNotFound(err) {
			// find stateful set
			// find rediscluster
			// delete pod from cluster
			r.Log.Info("Failed to get a pod", "namespacedname", req.NamespacedName)
		}
		return ctrl.Result{}, err
	}
	nsNameCluster := types.NamespacedName{
		Name:      pod.GetLabels()["rediscluster"],
		Namespace: req.Namespace,
	}
	err, redisCluster := r.FindRedisCluster(ctx, nsNameCluster)
	if err != nil {
		r.Log.Error(err, "can't find redis cluster", "nsname", nsNameCluster)
	}
	// get status
	ready := false
	for _, v := range pod.Status.Conditions {
		if v.Type == corev1.PodReady && v.Status == corev1.ConditionTrue {
			r.Log.Info("Pod status ready", "podname", pod.Name, "conditions", pod.Status.Conditions)
			ready = true
		}
	}
	if !ready {
		r.Log.Info("Pod status not ready", "podname", pod.Name)
		return ctrl.Result{}, nil
	}
	r.AddPodtoList(pod)
	clusterMeet := meet[pod.GetLabels()["rediscluster"]]
	clusterMeet.PodsNames[pod.GetName()] = pod.Status.PodIP

	redisCluster.Status.RedisClusterPods.Pods[pod.GetName()] = pod.Status.PodIP
	r.Log.Info("Current state", "state", clusterMeet)
	r.ClusterMeet(fmt.Sprintf("%s:%d", pod.Status.PodIP, redis.RedisCommPort), clusterMeet) //

	if len(clusterMeet.PodsNames) == int(redisCluster.Spec.Replicas) {
		r.AssignSlots(clusterMeet)
	}

	return ctrl.Result{}, nil
}

func (r *PodReconciler) AddPodtoList(pod *corev1.Pod) {
	rediscluster := pod.GetLabels()["rediscluster"]
	if _, exists := meet[rediscluster]; !exists {
		pods := make(map[string]string, 0)
		pods[pod.GetName()] = ""
		meet[rediscluster] = &RedisMeet{
			PodsNames: pods,
		}
		r.Log.Info("New state in CreateFunc", "state", meet)
	}
}

func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithEventFilter(r.PreFilter()).
		Complete(r)
}

func (r *PodReconciler) PreFilter() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			ours := isOwnedByUs(e.ObjectNew)
			if !ours {
				return false
			}
			return true
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			if isOwnedByUs(e.Object) {
				r.DeletePodFromList(e.Object)
			}
			return true
		},
	}
}

func (r *PodReconciler) DeletePodFromList(o client.Object) {
	rediscluster := o.GetLabels()["rediscluster"]

	if redismeet, exists := meet[rediscluster]; exists {
		delete(redismeet.PodsNames, o.GetName())
	}
	r.Log.Info("New state in DeleteFunc", "state", meet)
}

func isOwnedByUs(o client.Object) bool {
	labels := o.GetLabels()
	if _, found := labels["rediscluster"]; found {
		return true
	}
	return false
}

func (r *PodReconciler) ClusterMeet(endpoint string, meet *RedisMeet) {
	ctx := context.Background()
	rdb := redisclient.NewClient(&redisclient.Options{
		Addr:     endpoint,
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	for _, v := range meet.PodsNames {
		r.Log.Info("Running cluster meet", "node", v, "endpoint", endpoint)
		err := rdb.ClusterMeet(ctx, v, strconv.Itoa(redis.RedisCommPort)).Err()
		if err != nil {
			r.Log.Error(err, "clustermeet failed", "nodes", meet.PodsNames)
		}
	}
}

func (r *PodReconciler) AssignSlots(meet *RedisMeet) {
	// when all nodes are formed in a cluster, addslots
	slots := redis.SplitNodeSlots(len(meet.PodsNames))
	ctx := context.Background()
	i := 0
	for _, endpoint := range meet.PodsNames {
		rdb := redisclient.NewClient(&redisclient.Options{
			Addr:     fmt.Sprintf("%s:%d", endpoint, redis.RedisCommPort),
			Password: "", // no password set
			DB:       0,  // use default DB
		})

		rdb.ClusterAddSlotsRange(ctx, slots[i].Start, slots[i].End)
		r.Log.Info("Running cluster assign slots", "pods", meet)
		i++
	}
}
