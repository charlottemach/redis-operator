module github.com/containersolutions/redis-operator

go 1.15

require (
	github.com/go-logr/logr v0.3.0
	github.com/go-redis/redis/v8 v8.8.0
	github.com/google/go-cmp v0.5.5 // indirect
	github.com/joho/godotenv v1.3.0
	github.com/onsi/ginkgo v1.15.0
	github.com/onsi/gomega v1.10.5
	github.com/stretchr/testify v1.7.0 // indirect
	golang.org/x/tools v0.1.0 // indirect
	k8s.io/api v0.19.2
	k8s.io/apimachinery v0.19.2
	k8s.io/client-go v0.19.2
	k8s.io/utils v0.0.0-20200912215256-4140de9c8800
	sigs.k8s.io/controller-runtime v0.7.2
)
