package redis

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/containersolutions/redis-operator/api/v1alpha1"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
)

const RedisCommPort = 6379
const RedisGossPort = 16379
const RedisClusterLabel = "redis-cluster-name"

const TotalClusterSlots = 16384

type NodesSlots struct {
	Start int
	End   int
}

func CreateStatefulSet(ctx context.Context, req ctrl.Request, spec v1alpha1.RedisClusterSpec, labels map[string]string) *v1.StatefulSet {
	//	req ctrl.Request, replicas int32, redisImage string, storage string
	redisImage := spec.Image
	storage := spec.Storage
	replicas := spec.Replicas

	if redisImage == "" {
		redisImage = "redislabs/redisgraph:2.4.1"
	}
	if storage == "" {
		storage = "12Gi"
	}
	redisStatefulSet := &v1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels:    labels,
		},
		Spec: v1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{RedisClusterLabel: req.Name},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{
					Name: "data",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse(storage),
						},
					},
				}},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{RedisClusterLabel: req.Name, "app": "redis"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "redis-graph",
							Image: redisImage,
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
							Command:        []string{"redis-server", "/conf/redis.conf"},
							LivenessProbe:  CreateProbe(20, 5),
							ReadinessProbe: CreateProbe(15, 5),
							Resources: corev1.ResourceRequirements{
								Limits:   corev1.ResourceList{},
								Requests: corev1.ResourceList{},
							},
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
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "data",
								},
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

func CreateService(Namespace, Name string, labels map[string]string) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind: "Service", APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      Name,
			Namespace: Namespace,
			Labels:    labels,
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
			Selector:  map[string]string{RedisClusterLabel: Name, "app": "redis"},
			ClusterIP: "None",
		},
	}
}

func ConfigStringToMap(config string) map[string]string {
	//	strings.Split(config, b)
	nconfig := make(map[string]string)
	newlinestring := strings.Split(strings.ReplaceAll(config, "\r\n", "\n"), "\n")

	for _, v := range newlinestring {
		kv := strings.SplitN(v, ` `, 2)
		if strings.TrimSpace(kv[0]) != "" {
			nconfig[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return nconfig
}

func MapToConfigString(config map[string]string) string {
	bynline := make([]string, 0)
	for k, v := range config {
		bynline = append(bynline, fmt.Sprintf("%s %s", k, v))
	}

	return strings.Join(bynline, "\n")
}

func DefaultConfig() map[string]string {
	config := make(map[string]string)
	config["maxmemory"] = "1600mb"
	config["maxmemory-samples"] = "5"
	config["maxmemory-policy"] = "allkeys-lru"
	config["appendonly"] = "yes"
	config["protected-mode"] = "no"
	config["dir"] = "/data"
	config["cluster-enabled"] = "yes"
	config["cluster-require-full-coverage"] = "no"
	config["cluster-node-timeout"] = "15000"
	config["cluster-config-file"] = "/data/nodes.conf"
	config["cluster-migration-barrier"] = "1"
	return config
}

func MergeWithDefaultConfig(custom map[string]string) map[string]string {
	merged := custom
	defaultConfig := DefaultConfig()

	overrideNotAllowed := map[string]bool{"dir": true, "cluster-enabled": true, "cluster-require-full-coverage": true, "cluster-node-timeout": true, "cluster-config-file": true}
	for k := range custom {
		if overrideNotAllowed[k] {
			merged[k] = defaultConfig[k]
		}
	}

	for k, v := range defaultConfig {
		if merged[k] == "" {
			merged[k] = v
		}
	}

	return merged
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
	slotsNode, resto := slotsPerNode(numOfNodes, TotalClusterSlots)
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

func ConvertRedisMemToMbytes(maxMemory string) (int, error) {
	maxMemory = strings.ToLower(maxMemory)
	var maxMemoryInt int
	var err error
	if strings.Contains(maxMemory, "kb") || strings.Contains(maxMemory, "k") {
		maxMemory = strings.Replace(maxMemory, "kb", "", 1)
		maxMemory = strings.Replace(maxMemory, "k", "", 1)
		maxMemoryInt, err = strconv.Atoi(maxMemory)
		maxMemoryInt = maxMemoryInt / 1024

	} else if strings.Contains(maxMemory, "mb") || strings.Contains(maxMemory, "m") {
		maxMemory = strings.Replace(maxMemory, "mb", "", 1)
		maxMemory = strings.Replace(maxMemory, "m", "", 1)
		maxMemoryInt, err = strconv.Atoi(maxMemory)
	} else if strings.Contains(maxMemory, "gb") || strings.Contains(maxMemory, "g") {
		maxMemory = strings.Replace(maxMemory, "gb", "", 1)
		maxMemory = strings.Replace(maxMemory, "g", "", 1)
		maxMemoryInt, err = strconv.Atoi(maxMemory)
		maxMemoryInt = maxMemoryInt * 1024
	} else {
		maxMemoryInt, err = strconv.Atoi(maxMemory)
		maxMemoryInt = maxMemoryInt / 1024 / 1024
	}
	return maxMemoryInt, err
}

func GetClusterInfo(state string) map[string]string {
	lines := strings.Split(strings.ReplaceAll(state, "\r\n", "\n"), "\n")
	clusterstate := make(map[string]string)
	for _, line := range lines {
		kvmap := strings.Split(line, ":")
		if len(kvmap) == 2 {
			clusterstate[kvmap[0]] = kvmap[1]
		}
	}
	return clusterstate
}
