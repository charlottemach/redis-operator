![CI/CD](https://github.com/ContainerSolutions/redis-operator/actions/workflows/main.yml/badge.svg)

# Kubernetes and Openshift Redis Operator

## Purpose

Quickly provision Redis cluster environments in Kubernetes or Openshift.
The operator relies on Redis cluster functionality to serve client requests.

## How to run
To quickly run against an existing cluster, `config` folder contains all necessary resources

`config/ops/rbac` - RBAC manifests
`config/ops/crd` - CRD manifests
`config/apps/` - Operator deployment
`config/samples/` - Example RedisCluster deployment

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
