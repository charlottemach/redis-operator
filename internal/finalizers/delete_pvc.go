package finalizer

import (
	"context"

	"github.com/containersolutions/redis-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DeletePVCFinalizer struct {
}

func (ef *DeletePVCFinalizer) DeleteMethod(ctx context.Context, redis *v1alpha1.RedisCluster, client client.Client) error {
	claim := &corev1.PersistentVolumeClaim{}
	err := client.Delete(ctx, claim)
	return err
}

func (ef *DeletePVCFinalizer) GetId() string {
	return "redis.containersolutions.com/delete-pvc"
}
