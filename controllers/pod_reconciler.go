package controllers

import (
	"context"
	"fmt"
	"time"

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
	time.Sleep(time.Second * 10)
	pod := &corev1.Pod{}
	err := r.Client.Get(ctx, req.NamespacedName, pod)

	if err != nil {
		if !errors.IsNotFound(err) {
			r.Log.Error(err, "Failed to get a pod", "namespacedname", req.NamespacedName)
		}
		return ctrl.Result{}, err
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
	r.ClusterMeet(fmt.Sprintf("%s:%s", pod.Status.PodIP, REDIS_PORT), meet[pod.GetLabels()["rediscluster"]].PodsNames)

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
			return false
			//			return isOwnedByUs(e.ObjectNew)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			ours := isOwnedByUs(e.Object)
			if !ours {
				return false
			}

			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			ours := isOwnedByUs(e.Object)
			if !ours {
				return false
			}
			rediscluster := e.Object.GetLabels()["rediscluster"]

			if redismeet, exists := meet[rediscluster]; exists {
				delete(redismeet.PodsNames, e.Object.GetName())
			}
			r.Log.Info("New state in DeleteFunc", "state", meet)
			return true

		},
	}
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
