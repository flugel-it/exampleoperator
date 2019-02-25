# exampleoperator

This repository implements an example Kubernetes operator, called "ImmortalContainers". It's based on https://github.com/kubernetes/sample-controller.
This operator enables the user to define, using custom resources, containers that must run and if
terminated must be restarted.

## Building

```bash
$ git clone git@github.com:flugel-it/exampleoperator.git $GOPATH/src/github.com/flugel-it/exampleoperator

$ make
```

## Install CRD and RBAC permissions

To install CRDs and RBAC configurations to your currently set cluster use:

```bash
make install
```

## Running the operator outside the cluster

```bash
./exampleoperator --kubeconfig ~/.kube/config
```

## Running inside the cluster

You must first generate the image using `make docker-build` and push it to your repo.

If using **minikube** follow these steps:

```bash
eval $(minikube docker-env)
make docker-build
```

Then create the `system` namespace

```bash
kubectl apply -f config/namespace.yaml
```

And then run `make deploy`.

After this you should check that everything is running, ex:

```bash
$ kubectl get pods --namespace system                     
NAME                                          READY   STATUS    RESTARTS   AGE
exampleoperator-controller-7cb7f99658-97zjs   1/1     Running   0          24m

$ kubectl logs exampleoperator-controller-7cb7f99658-97zjs --namespace=system

I0221 19:47:51.036187       1 controller.go:115] Setting up event handlers
I0221 19:47:51.036509       1 controller.go:157] Starting ImmortalContainer controller
I0221 19:47:51.038034       1 controller.go:160] Waiting for informer caches to sync
I0221 19:47:51.138621       1 controller.go:165] Starting workers
I0221 19:47:51.138939       1 controller.go:171] Started workers
I0221 19:47:51.139358       1 controller.go:229] Successfully synced 'default/example-immortal-container'
```

## Using the operator

Once the operator is running you can create immortal containers using a custom resource like this one:

```yaml
apiVersion: exampleoperator.flugel.it/v1alpha1
kind: ImmortalContainer
metadata:
  name: example-immortal-container
spec:
  image: nginx:latest
```

Run `kubectl apply -f config/example-use.yaml` to try it.

Then run `kubectl get pods` and check the pod is created. If you kill the pod it will be recreated.

## Remove the operator

To remove the operator, CDR and RBAC use `make undeploy`

Pods created by the operator will not be deleted, but will not be restarted if deleted later.
