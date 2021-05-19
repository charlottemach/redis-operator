package finalizer

import (
	"context"

	"github.com/containersolutions/redis-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type BackupFinalizer struct {
}

func (ef *BackupFinalizer) DeleteMethod(ctx context.Context, redis *v1alpha1.RedisCluster, client client.Client) error {
	// final backup before deletion
	return nil
}

func (ef *BackupFinalizer) GetId() string {
	return "redis.containersolutions.com/rdb-backup"
}
