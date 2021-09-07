module github.com/containersolutions/redis-operator

go 1.15

require (
	github.com/garyburd/redigo v1.6.2
	github.com/go-logr/logr v0.3.0
	github.com/go-redis/redis/v8 v8.8.0
	github.com/joho/godotenv v1.3.0
	github.com/onsi/ginkgo v1.15.0
	github.com/onsi/gomega v1.10.5
	github.com/sirupsen/logrus v1.6.0
	github.com/urfave/cli v1.20.0
	golang.org/x/tools v0.1.0 // indirect
	k8s.io/api v0.19.2
	k8s.io/apimachinery v0.19.2
	k8s.io/client-go v0.19.2
	k8s.io/utils v0.0.0-20200912215256-4140de9c8800
	sigs.k8s.io/controller-runtime v0.7.2
)
