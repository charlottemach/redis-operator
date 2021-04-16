package redis

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
)

const RedisCommPort = 6379
const RedisGossPort = 16379

const totalClusterSlots = 16384

type NodesSlots struct {
	Start int
	End   int
}

func CreateStatefulSet(ctx context.Context, req ctrl.Request, replicas int32) *v1.StatefulSet {
	redisStatefulSet := &v1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
		Spec: v1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"rediscluster": req.Name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"rediscluster": req.Name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "redis-graph",
							Image: "redislabs/redisgraph:2.4.1",
							Ports: []corev1.ContainerPort{
								{
									Name:          "client",
									ContainerPort: RedisCommPort,
								},
								{
									Name:          "gossip",
									ContainerPort: RedisGossPort,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "config",
									MountPath: "/conf",
								},
								{
									Name:      "data",
									MountPath: "/data",
								},
							},
							Command:        []string{"redis-server", "/conf/redis.conf", "--loadmodule", "/usr/lib/redis/modules/redisgraph.so"},
							LivenessProbe:  CreateProbe(20, 5),
							ReadinessProbe: CreateProbe(15, 5),
						},
					},

					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: req.Name},
									Items:                []corev1.KeyToPath{{Key: "redis.conf", Path: "redis.conf"}},
								},
							},
						},
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}
	return redisStatefulSet
}

func CreateProbe(initial int32, period int32) *corev1.Probe {
	return &corev1.Probe{
		Handler: corev1.Handler{
			Exec: &corev1.ExecAction{
				Command: []string{"sh", "-c", "redis-cli -h $(cat /etc/hostname) -c ping"},
			},
		},
		InitialDelaySeconds: initial,
		PeriodSeconds:       period,
	}
}

func CreateService(Namespace, Name string) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind: "Service", APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      Name,
			Namespace: Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:     "client",
					Protocol: "TCP",
					Port:     RedisCommPort,
					TargetPort: intstr.IntOrString{Type: 0,
						IntVal: RedisCommPort,
					},
				},
				{
					Name:     "gossip",
					Protocol: "TCP",
					Port:     RedisGossPort,
					TargetPort: intstr.IntOrString{Type: 0,
						IntVal: RedisGossPort,
					},
				},
			},
			Selector: map[string]string{"rediscluster": Name},
		},
	}
}

func slotsPerNode(numOfNodes int, slots int) (int, int) {
	if numOfNodes == 0 {
		return 0, 0
	}
	slotsNodes := slots / numOfNodes
	resto := slots % numOfNodes
	return slotsNodes, resto
}

func SplitNodeSlots(nodesTotal int) []*NodesSlots {
	nodesSlots := []*NodesSlots{}
	numOfNodes := nodesTotal
	slotsNode, resto := slotsPerNode(numOfNodes, totalClusterSlots)
	slotsAsigment := []string{}
	for i := 0; i < numOfNodes; i++ {
		if i == 0 {
			slotsAsigment = append(slotsAsigment, fmt.Sprintf("0,%s", strconv.Itoa(slotsNode-1)))
		} else if i == 1 {
			slotsAsigment = append(slotsAsigment, fmt.Sprintf("%s,%s", strconv.Itoa(slotsNode*i), strconv.Itoa(slotsNode*(i+1)-1)))
		} else if i == numOfNodes-1 {
			slotsAsigment = append(slotsAsigment, fmt.Sprintf("%s,%s", strconv.Itoa(slotsNode*i), strconv.Itoa(slotsNode*(i+1)-1+resto)))
		} else {
			slotsAsigment = append(slotsAsigment, fmt.Sprintf("%s,%s", strconv.Itoa((slotsNode*i)), strconv.Itoa(slotsNode*(i+1)-1)))
		}
	}
	for i := 0; i < numOfNodes; i++ {
		stringRangeSplit := strings.Split(slotsAsigment[i], ",")
		start, err := strconv.Atoi(stringRangeSplit[0])
		if err != nil {
			log.Printf("Error assignSlotsToNodes: strconv.Atoi(stringRangeSplit[0] - %s", err)
		}

		end, err := strconv.Atoi(stringRangeSplit[1])
		if err != nil {
			log.Printf("Error assignSlotsToNodes: strconv.Atoi(stringRangeSplit[1] - %s", err)
		}
		nodesSlots = append(nodesSlots, &NodesSlots{start, end})
	}
	return nodesSlots
}
