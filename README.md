# ns-operator

`ns-operator` is a Kubernetes controller that implements `NamespaceClass`, a cluster-scoped CRD (`namespaceclass.akuity.io`). Users declare an arbitrary list of resource manifests in `NamespaceClass.spec.resources`, then label a `Namespace` with `namespaceclass.akuity.io/name: <class-name>`. The controller then creates those resources in that namespace, and keeps them in sync as the label or the manifests change. `NamespaceClass.status.observedKinds` accumulates every resource Kind the class has ever declared, even after that Kind is later removed from `spec.resources`.

## Deployment

### Build your own image (Optional)

```bash
docker build -t <your-registry>/ns-operator:latest .
docker push <your-registry>/ns-operator:latest
```

After building, update the `image` field in `config/manager/deployment.yaml` to point at your own image.

### Deploy to the cluster

```bash
make deploy
```

This applies, in order:

1. the `ns-operator-system` namespace (`config/manager/namespace.yaml`);
2. the `NamespaceClass` CRD (`config/crd/`);
3. the ServiceAccount and RBAC (`config/rbac/`);
4. the operator Deployment (`config/manager/deployment.yaml`).

### Verify the deployment

```bash
kubectl -n ns-operator-system get deployment ns-operator
kubectl get crd namespaceclasses.namespaceclass.akuity.io
```

The deployment should report `1/1` ready replicas, `rollout status` should report a successful rollout, and the CRD should exist. You can also check the pod logs with:

```bash
kubectl -n ns-operator-system logs -l app=ns-operator
```

### Uninstall

```bash
make clean
```

This removes everything deployed above (Deployment, RBAC, CRD, namespace).

## Running the E2E tests

The e2e suite assumes the operator and its CRD are already deployed to the cluster the current `kubectl` context points at, so run `make deploy` first.

```bash
make deploy
make e2e-test
```

`make e2e-test` is equivalent to:

```bash
go test -tags e2e ./test/e2e/... -v -timeout 5m
```

The tests connect to the cluster using the `KUBECONFIG` environment variable (falling back to `~/.kube/config` if unset), then create `NamespaceClass` objects and labeled `Namespace`s and assert that the controller creates/prunes the expected resources and updates `status.observedKinds` accordingly.
