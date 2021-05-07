# Kubernetes and Openshift Redis Operator

## Purpose

Quickly provision Redis cluster environments in Kubernetes or Openshift.
The operator relies on Redis cluster functionality to server client requests.

## How to run
To quickly run against an existing cluster, `config` folder contains all necessary resources

## Tests

There are some basic tests validating deployments.
Tests can be run with:
```
make test
```

## Features

Features milestone:
* POC
 * [x] Garbage collection
 * [x] Health checks
 * [X] Assign slots
 * [X] Set watcher with filter for redis instances
 * [X] Once instance is up and running, introduce it to other instances
 * [X] Once all instances are up and running, addslots
 * [X] Form a cluster
 * [X] Add redisgraph support
* Early alpha / beta
 * [x] Check that secrets are passed properly
 * [x] Authentication suppor
 * [x] Build pipeline
 * [x] Kafka metrics streams
 * [x] Generate events
   - When slots assign is complete
   - When cluster is healthy / unhealthy
 * [x] Annotations and labels 
* Later
 * [ ] Dynamic scaling
 * [ ] Periodic backup
