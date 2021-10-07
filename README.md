![CI/CD](https://github.com/ContainerSolutions/redis-operator/actions/workflows/main.yml/badge.svg)

# Kubernetes and Openshift Redis Operator

## Purpose

Quickly provision Redis cluster environments in Kubernetes or Openshift.
The operator relies on Redis cluster functionality to serve client requests.

## Folder structure

The `config` folder contains all necessary kubernetes and [kustomize](https://kustomize.io) manifests

Manifests:
* `config/ops/rbac` - RBAC manifests
* `config/ops/crd` - CRD manifests
* `config/apps/` - Operator deployment
* `config/samples/` - Example RedisCluster deployment

## How to run the operator

1. Create a namespace to deploy your redis operator in

```
kubectl create ns my-redis-operator
```

2. Change the namespace in kustomize config
    1. In `config/apps/kustomization.yaml` set `namespace` to `my-redis-operator`
    2. In `config/ops/rbac/role_binding.yaml` set the `ServiceAccount` namespace to `my-redis-operator`

3. Deploy the operator and a sample `RedisCluster` resource:

```
kustomize build config/ops/crd | kubectl apply -f -
kustomize build config/ops/rbac | kubectl apply -f -
kustomize build config/apps | kubectl apply -f -
kustomize build config/samples/ | kubectl apply -f -
```

## Cleanup
Delete the operator and all associated resources with:

```
kustomize build config/samples/ | kubectl delete -f -
kustomize build config/apps | kubectl delete -f -
kustomize build config/ops/rbac | kubectl delete -f -
kustomize build config/ops/crd | kubectl delete -f -
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
