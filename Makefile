OUT?=exampleoperator
IMG?=exampleoperator:dev

build: dep generatecode
	CGO_ENABLED=0 GOOS=linux go build -o ${OUT}

dep:
	go get sigs.k8s.io/controller-tools/cmd/controller-gen

generatecode:
	./hack/update-codegen.sh

test:
	go test

clean:
	rm ${OUT}
	
docker-build:
	docker build . -t ${IMG}

manifest: dep
	go run ${GOPATH}/src/sigs.k8s.io/controller-tools/cmd/controller-gen/main.go all

# Install CRDs and RBACs into a cluster
install: manifest
	kubectl apply -f config/crds
	kubectl apply -f config/rbac

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: install
	kubectl apply -f config/default --namespace=system

# Remove controller in the configured Kubernetes cluster in ~/.kube/config
undeploy: manifest
	kubectl delete -f config/default --namespace=system
	kubectl delete -f config/crds || true
	kubectl delete -f config/rbac || true
