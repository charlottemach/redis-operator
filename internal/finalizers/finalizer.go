package finalizer

import (
	"context"

	"github.com/containersolutions/redis-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Finalizer interface {
	DeleteMethod(context.Context, *v1alpha1.RedisCluster, client.Client) error
	GetId() string
}
