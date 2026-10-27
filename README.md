# Quarters

A Kubernetes operator that manages namespaces. One `Tenant` resource creates a
namespace and everything the platform configures inside it: labels, Pod
Security Admission, quota, limit range, Cilium network policies and node
placement through OPA Gatekeeper. It replaces `ns-config`, an internal Helm chart that created each namespace
from a `values.yaml` block (quota, limit range, Pod Security labels and
Cilium network policies).

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
| Network | `CiliumNetworkPolicy <ns>-{allow,deny}-{ingress,egress}`; deny-all unless something is allowed. `network.podPolicies` adds policies for the pods carrying one label (`<ns>-<name>-{allow,deny}-{ingress,egress}`); a direction they allow is exempted from the namespace-wide deny |
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

Releases are published to GitHub Container Registry by the `release.yml` workflow when a `v*.*.*` tag is pushed:

| Artifact | Location |
|---|---|
| Operator image (amd64, arm64) | `ghcr.io/pinginfo/quarters:<version>` |
| Operator chart | `oci://ghcr.io/pinginfo/charts/quarters` |
| Tenants chart | `oci://ghcr.io/pinginfo/charts/quarters-tenants` |

Prerequisites: cert-manager (webhook certificates), Cilium, and Gatekeeper
(optional until you use `scheduling`). Gatekeeper should run with mutation
enabled and webhook `failurePolicy: Fail`; see the architecture doc.

```sh
VERSION=0.4.0

# 1. the operator: CRDs, RBAC, 2 replicas, webhook, metrics
helm upgrade --install quarters oci://ghcr.io/pinginfo/charts/quarters --version $VERSION \
  -n quarters-system --create-namespace

# 2. the Gatekeeper ConstraintTemplate, once Gatekeeper runs (only needed for `scheduling`)
kubectl apply -k "https://github.com/pinginfo/quarters/config/gatekeeper?ref=v$VERSION"

# 3. a TenantProfile and your tenants (see below)
helm upgrade --install tenants oci://ghcr.io/pinginfo/charts/quarters-tenants --version $VERSION \
  -n quarters-system -f my-tenants.yaml
```

The published operator chart already points at the matching image, so no `--set manager.image.*` is needed.
New GHCR packages are private: make the image and both charts public in the package settings of the
repository, or create an image pull secret and set `manager.imagePullSecrets`.
`helm show values oci://ghcr.io/pinginfo/charts/quarters --version $VERSION` lists every option.

Useful operator values: `manager.replicas` (default 2), `manager.imagePullSecrets`,
`certManager.enabled`, `prometheus.enabled`, `crd.keep`. The CRDs are templates of the chart, so
`helm upgrade` updates them, and `crd.keep: true` (default) keeps them, and so your Tenants, when the release is
uninstalled. If Cilium or Gatekeeper CRDs are installed after the operator started, they are picked up within
10 minutes (periodic resync) or on restart.

### Tenants chart

`quarters-tenants` only renders `TenantProfile` and `Tenant` objects from a values file. It ships the
`default` profile, so a minimal `my-tenants.yaml` is:

```yaml
tenants:
  app-production:
    team: backend
    quota: {pods: "50"}
    network:
      ingress: {allowSameNamespace: true}
```

`profiles` and `tenants` are maps keyed by name, and each entry is the `spec` as in the CRD
(plus `adopt: true` on a tenant to set the adopt annotation). `values.schema.json` rejects a missing
`team`, a bad quantity such as `"50h"` or a misspelled field at `helm lint`/`template` time, before the webhook does.
Objects carry `helm.sh/resource-policy: keep` and Argo CD `Prune=false` (`commonAnnotations`), so removing
a tenant from the file never deletes it: use `kubectl delete tenant` for that. Profiles are applied in
Argo CD sync wave 0 and tenants in wave 1. The chart is optional, plain `Tenant` YAML in Git works as well
(`config/samples/`). With Argo CD, point an Application at the OCI chart or at your own Git repository.

### Releasing

```sh
git tag v0.4.0 && git push origin v0.4.0
```

`ci.yml` runs on pull requests and pushes (vet, tests, `helm lint`, an unpushed multi-arch image build).
`release.yml` runs the tests, pushes the image, then packages both charts with the tag as chart and app version
and pushes them. The workflow needs permission to write packages (Settings, Actions, Workflow permissions).

### From source

For development, or without GHCR. The Makefile uses docker, or podman if docker is missing.

```sh
make docker-build docker-push IMG=<registry>/quarters:<tag>
make deploy IMG=<registry>/quarters:<tag>               # needs Go and kustomize on the host
kubectl apply -k config/gatekeeper
kubectl apply -f config/samples/profile-default.yaml
```

Or install the in-repo charts, which need only `helm`:

```sh
helm upgrade --install quarters dist/chart -n quarters-system --create-namespace \
  --set manager.image.repository=<registry>/quarters --set manager.image.tag=<tag>
helm upgrade --install tenants dist/tenants-chart -n quarters-system -f my-tenants.yaml
```

`dist/chart` is generated: after changing the API, webhooks or `config/`, run `make build-installer`
and `kubebuilder edit --plugins=helm/v2-alpha --force`, then restore the local edits in
`dist/chart/values.yaml` (`manager.replicas: 2`) and delete the `.github/` the plugin recreates.
`make helm-deploy IMG=...` wraps `helm upgrade --install`. A gitignored `values.yaml` in the repository root
is a convenient place for local overrides: `helm upgrade --install quarters dist/chart -f values.yaml`.

## Develop

Requires Go and, for the integration tests, the envtest binaries
(`setup-envtest use 1.36`). The Makefile is the standard Kubebuilder one.

```sh
make generate manifests   # deepcopy, CRDs, RBAC, webhook config
make test                 # unit tests + envtest against a real API server
```

`test/crds/` holds minimal stand-ins for the Cilium and Gatekeeper CRDs used by envtest.

## Metrics and dashboard

Besides the controller-runtime metrics, Quarters exports:

| Metric | Labels | Meaning |
|---|---|---|
| `quarters_tenant_info` | tenant, team, profile, deletion_policy | always 1 |
| `quarters_tenant_phase` | tenant, phase | 1 for the current phase (Ready, Degraded, Terminating) |
| `quarters_tenant_condition` | tenant, type, status | 1 for the current status of each condition |
| `quarters_tenant_condition_last_transition_timestamp_seconds` | tenant, type | when a condition last changed |
| `quarters_tenant_generation_lag` | tenant | spec generation not yet reconciled |
| `quarters_module_reconcile_duration_seconds` | module | histogram |
| `quarters_module_errors_total` | module | counter |

The tenant metrics are read from the informer cache at scrape time and only the elected leader
answers, so deleted tenants disappear at once and each tenant appears once.

Two Grafana dashboards are in `config/grafana`:

- `quarters-overview.json`, for DevOps: the fleet (counts by phase, a table of every tenant with problems
  first, module status per tenant, operator health). Filters: team, profile, tenant.
- `quarters-capacity.json`, for the cluster owner: how the cluster is divided. Quota allowed per tenant
  and team as % of the whole cluster (CPU, memory, pods), how much is promised in total against what
  nodes can offer, how full each tenant's own quota is, and reserved versus really used (where quotas
  can be reduced).

Load them by importing the JSON in Grafana, or with the Grafana sidecar:

```sh
kubectl apply -k config/grafana -n monitoring     # ConfigMap labelled grafana_dashboard=1
```

Both need Prometheus to scrape the operator. The quota columns of the first and most of the second need
kube-state-metrics (`kube_resourcequota`, `kube_node_status_allocatable`, pod requests); "really used"
needs cAdvisor (`container_*`, scraped from the kubelet by default in kube-prometheus-stack).
Capacity per node location would need kube-state-metrics to export your node label
(`--metric-labels-allowlist=nodes=[location]`); the dashboards do not assume it. With prometheus-operator, enable the chart's ServiceMonitor
(`--set prometheus.enabled=true`). The metrics endpoint is HTTPS and authenticated, so give the
Prometheus service account the `quarters-metrics-reader` ClusterRole.

## License

Apache License 2.0, see [LICENSE](LICENSE).
