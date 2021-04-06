package redis

import (
	"context"

	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
)

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
									ContainerPort: 6379,
								},
								{
									Name:          "gossip",
									ContainerPort: 16379,
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
					Port:     6379,
					TargetPort: intstr.IntOrString{Type: 0,
						IntVal: 6379,
					},
				},
				{
					Name:     "gossip",
					Protocol: "TCP",
					Port:     16379,
					TargetPort: intstr.IntOrString{Type: 0,
						IntVal: 16379,
					},
				},
			},
			Selector: map[string]string{"rediscluster": Name},
		},
	}
}

/*
   liveness_probe {
           exec {
             command = ["sh", "-c", "redis-cli -h $(cat /etc/hostname) -c ping"]
           }

           initial_delay_seconds = 20
           period_seconds        = 3
         }

         readiness_probe {
           exec {
             command = ["sh", "-c", "redis-cli -h $(cat /etc/hostname) -c ping"]
           }

           initial_delay_seconds = 15
           timeout_seconds       = 5
         }
*/
