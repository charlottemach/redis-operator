# redis-operator

Quickly provision Redis cluster environments in Kubernetes or Openshift.
The operator relies on Redis cluster functionality to server client requests.

# Features

Features milestone:
* POC
 * DONE Garbage collection
 * DONE Health checks
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
 * [ ] Kafka metrics streams
 * [ ] Generate events
   - When slots assign is complete
   - When cluster is healthy / unhealthy
 * TODO Annotations and labels 
* Later
 * Dynamic scaling
 * Periodic backup
