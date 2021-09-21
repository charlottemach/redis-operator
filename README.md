![CI/CD](https://github.com/ContainerSolutions/redis-operator/actions/workflows/main.yml/badge.svg)

# Kubernetes and Openshift Redis Operator

## Purpose

Quickly provision Redis cluster environments in Kubernetes or Openshift.
The operator relies on Redis cluster functionality to serve client requests.

## How to run
To quickly run against an existing cluster, `config` folder contains all necessary resources

Manifests:
* `config/ops/rbac` - RBAC manifests
* `config/ops/crd` - CRD manifests
* `config/apps/` - Operator deployment
* `config/samples/` - Example RedisCluster deployment

Deploy the operator and a sample RedisCluster resource:

```
kustomize build config/ops/crd | kubectl apply -f -
kustomize build config/ops/rbac | kubectl apply -f -
kustomize build config/apps | kubectl apply -f -
kustomize build config/samples/ | kubectl apply -f -
```


## Tests

There are some basic tests validating deployments.
Tests can be run with:
```
make test
```

## Features

* Cluster creation
* Slots allocation
* PVC
* Sidecar pods support (e.g. custom metrics applications)
* Authentication support
* Persistance
* RedisGraph support
