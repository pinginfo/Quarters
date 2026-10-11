# Quarters

A Kubernetes operator that manages namespaces. One `Tenant` resource creates a
namespace and everything the platform configures inside it: labels, Pod
Security Admission, quota, limit range, Cilium network policies and node
placement through OPA Gatekeeper. It replaces the `ns-config` Helm chart.

- API group `quarters.pinginfo.io/v1alpha1`: `Tenant` and `TenantProfile`, both cluster-scoped.
- Built with Go and Kubebuilder (controller-runtime). Target: Kubernetes 1.36, Cilium 1.19, Gatekeeper, Talos.
- Design and decisions: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). Moving off Helm: [docs/migration-from-helm.md](docs/migration-from-helm.md).

## How it works

```yaml
apiVersion: quarters.pinginfo.io/v1alpha1
kind: Tenant
metadata:
  name: app-production          # also the namespace name
spec:
  profileRef: default           # defaults come from a TenantProfile
  team: backend                 # required
  deletionPolicy: Retain        # Delete | Retain (default Retain)
  quota: {pods: "50"}           # overrides the profile, key by key
  scheduling:
    nodeSelector: {mode: Force, labels: {location: site-a}}
```

The effective configuration is `TenantProfile` overlaid with the Tenant (maps
merge key by key, lists and scalars replace) and is stored in `status.resolved`.

One reconciler runs the **feature modules** in order. Each reports its own
condition (`NamespaceReady`, `QuotaReady`, `LimitRangeReady`, `NetworkReady`,
`SchedulingReady`) and the Tenant gets an overall `Ready`.

| Module | Creates |
|---|---|
| Namespace | the namespace, `team` labels, user labels/annotations, PSA labels |
| Quota | `ResourceQuota <ns>-quota` |
| LimitRange | `LimitRange <ns>-quota-limit-range` |
| Network | `CiliumNetworkPolicy <ns>-{allow,deny}-{ingress,egress}`; deny-all unless something is allowed |
| Scheduling | Gatekeeper `Assign` (mode `Force`) or `QuartersScheduling` constraint (mode `Allow`) |

Objects are applied with server-side apply, so manual edits to managed fields
are reverted, and objects a module stops asking for are deleted.

### Scheduling

Node selectors and tolerations are generic and independent. Each has a mode:

- `Force`: injected into every pod of the namespace, overriding the pod.
- `Allow`: pods may use only the listed values, anything else is rejected.
- `Disabled`: nothing happens.

Mutation only applies to pods created after the change; running pods keep their
placement until they restart. In `Allow` mode a toleration must match an item
exactly, except the `node.kubernetes.io/*` ones Kubernetes adds itself.

### Deleting a Tenant

`Retain` keeps the namespace and every object in it (they are released from the
Tenant). `Delete` deletes the namespace, but only if Quarters created or adopted it.

## Adding a feature

Implement `features.Feature` (`Name`, `Critical`, `Kinds`, `Reconcile`) in
`internal/features/`, build the desired objects, hand them to `Applier.Apply`,
and add the module to `features.All()`. Config fields go in `api/v1alpha1/config_types.go`;
validation rules go in `internal/validation`.

## Install

Prerequisites: cert-manager (webhook certificates), Cilium, and Gatekeeper
(optional until you use `scheduling`). Gatekeeper should run with mutation
enabled and webhook `failurePolicy: Fail`; see the architecture doc.

```sh
make docker-build docker-push IMG=<registry>/quarters:<tag>   # uses docker, or podman if docker is missing
make deploy IMG=<registry>/quarters:<tag>        # CRDs, RBAC, 2 replicas, webhook
kubectl apply -k config/gatekeeper                      # ConstraintTemplate, once Gatekeeper runs
kubectl apply -f config/samples/profile-default.yaml
```

### With Helm

A chart is generated from the same manifests in `dist/chart`. It needs no Go, only `helm` and cert-manager:

```sh
helm upgrade --install quarters dist/chart -n quarters-system --create-namespace \
  --set manager.image.repository=<registry>/quarters --set manager.image.tag=<tag>
```

Useful values: `manager.replicas` (default 2), `manager.imagePullSecrets` for a private registry,
`certManager.enabled`, `prometheus.enabled`, `crd.keep`. Install the Gatekeeper template from
`config/gatekeeper` separately. The CRDs are templates of the chart, so `helm upgrade` updates them, and `crd.keep: true`
(default) keeps them, and so your Tenants, when the release is uninstalled.

The chart is generated: after changing the API, webhooks or `config/`, run `make build-installer`
and `kubebuilder edit --plugins=helm/v2-alpha --force`, then restore the local edits in
`dist/chart/values.yaml` (`manager.replicas: 2`). `make helm-deploy IMG=...` wraps `helm upgrade --install`.

If Cilium or Gatekeeper CRDs are installed after the operator started, they are
picked up within 10 minutes (periodic resync) or on restart.

## Develop

Requires Go and, for the integration tests, the envtest binaries
(`setup-envtest use 1.36`). The Makefile is the standard Kubebuilder one.

```sh
make generate manifests   # deepcopy, CRDs, RBAC, webhook config
make test                 # unit tests + envtest against a real API server
```

`test/crds/` holds minimal stand-ins for the Cilium and Gatekeeper CRDs used by envtest.

## Metrics

Besides the controller-runtime metrics: `quarters_module_reconcile_duration_seconds{module}`,
`quarters_module_errors_total{module}`.
