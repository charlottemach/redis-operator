package controllers

import (
	"context"
	"fmt"

	//"golang.org/x/tools/godoc/redirect"

	//v1 "k8s.io/client-go/tools/clientcmd/api/v1"
	"github.com/go-logr/logr"
	redisclient "github.com/go-redis/redis/v8"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const REDIS_PORT = "6379"

type RedisMeet struct {
	PodsNames map[string]string
}

var meet = make(map[string]*RedisMeet)

type PodReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("rediscluster - pod reconciler", req.NamespacedName)
	r.Log.Info(fmt.Sprintf("Pod reconciler called - %s %s", req.Namespace, req.Name))
	pod := &corev1.Pod{}
	err := r.Client.Get(ctx, req.NamespacedName, pod)

	if err != nil {
		if !errors.IsNotFound(err) {
			r.Log.Error(err, "Failed to get a pod", "namespacedname", req.NamespacedName)
		}
		return ctrl.Result{}, err
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
		r.Log.Info("Pod status ready", "podname", pod.Name)
		return ctrl.Result{}, nil
	}

	rediscluster := pod.GetLabels()["rediscluster"]
	if _, exists := meet[rediscluster]; !exists {
		pods := make(map[string]string, 0)
		pods[pod.GetName()] = ""
		meet[rediscluster] = &RedisMeet{
			PodsNames: pods,
		}
		r.Log.Info("New state in CreateFunc", "state", meet)
	}
	meet[pod.GetLabels()["rediscluster"]].PodsNames[pod.GetName()] = pod.Status.PodIP

	r.Log.Info("Current state", "state", meet)
	r.ClusterMeet(fmt.Sprintf("%s:%s", pod.Status.PodIP, REDIS_PORT), meet[pod.GetLabels()["rediscluster"]].PodsNames) //

	return ctrl.Result{}, nil
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
			ours := isOwnedByUs(e.Object)
			if ours {
				r.DeletePodFromList(e.Object)
			}
			return false

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

func (r *PodReconciler) ClusterMeet(endpoint string, others map[string]string) {
	ctx := context.Background()
	rdb := redisclient.NewClient(&redisclient.Options{
		Addr:     endpoint,
		Password: "", // no password set
		DB:       0,  // use default DB
	})
	for _, v := range others {
		r.Log.Info("Running cluster meet", "node", v, "endpoint", endpoint)
		err := rdb.ClusterMeet(ctx, v, REDIS_PORT).Err()
		if err != nil {
			r.Log.Error(err, "clustermeet failed", "nodes", others)
		}
	}
}
