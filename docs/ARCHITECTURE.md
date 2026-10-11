# Quarters: architecture plan (draft v2)

Codename: **Quarters**, a namespace operator (each `Tenant` is a "quarter" of the cluster).
Repo: `github.com/pinginfo/quarters` (Go + Kubebuilder, controller-runtime)
API group: `quarters.pinginfo.io` (placeholder, rename freely).
Target platform: Kubernetes 1.36.x on Talos, Cilium 1.19.6, latest Gatekeeper (mutation required).

## 1. Goals
- Replace `ns-config`, an internal Helm chart that built each namespace from a `values.yaml` block: one `Tenant` CR per namespace creates the namespace and everything inside it.
- Extensible: a new feature (labeling, security, scheduling) is one new Go package and one registry line.
- Production from day one: HA, validating webhook, metrics, conditions, tests, adoption of Helm-managed namespaces.

## 2. What the current chart does (mapped to modules)
| Chart template | Operator module | Notes |
|---|---|---|
| `namespace.yaml` | `namespace` + `labels` | labels, annotations, `team` label; PSA labels to add |
| `quota.yaml` (ResourceQuota) | `quota` | defaults merged with per-namespace overrides |
| `quota.yaml` (LimitRange) | `limitrange` | hard-coded today (100m / 128Mi), make it configurable |
| `ciliumingresspolicy.yaml` | `network` | **CiliumNetworkPolicy**, not core NetworkPolicy |
| `ciliumegresspolicy.yaml` | `network` | includes FQDN and DNS-proxy rules, Cilium-only |

### Bugs and gaps found in the chart (fix during translation, don't copy)
1. `quota.yaml`: `count/statefullstates.apps` is a typo and should be `count/statefulsets.apps`. The quota key is silently wrong today.
2. `values.yaml`: `secrets: "50h"` is an invalid quantity (`h`). A CRD schema and the webhook would reject it. Helm accepted it.
3. `values.yaml`: `defaults.quota.replicationcontrollers` is defined but never rendered.
4. `egressPolicy.allowAllEgress` is in the values but no template reads it.
5. In egress, `allowEgressToCIDR: 0.0.0.0/0` for `media-test` allows everything, including cluster-internal ranges, so it defeats the namespace allowlist. Worth reviewing.
6. Cilium *deny* rules take precedence over *allow* rules. The chart only emits a deny policy when no allow exists, which is why it works. The operator must keep this invariant (either allow or deny-all, never both).
7. `empty-prod` gets deny-all ingress and egress. In the operator this becomes the default (secure by default) and is documented.
8. Storage limits appear in the defaults (`limits.storage`) but the template never renders them.

## 3. API
### `Tenant` (cluster-scoped, `v1alpha1`)
```yaml
apiVersion: quarters.pinginfo.io/v1alpha1
kind: Tenant
metadata:
  name: app-production        # = namespace name
spec:
  profileRef: default         # TenantProfile name
  team: backend               # REQUIRED (CRD schema + webhook); becomes label quarters.pinginfo.io/team + chart-compatible `team`
  labels: {environment: production, tier: application}
  annotations: {owner: backend-team@example.com, cost-center: cc-1001}
  deletionPolicy: Retain      # Delete | Retain
  quota:      {...}           # overrides, merged over the profile
  limitRange: {...}
  network:
    ingress:
      allowSameNamespace: true
      allowCiliumIngress: false
      allowFromNamespaces: [ingress-nginx, monitoring]
    egress:
      toNamespaces: [kube-system, shared-services]
      toCIDRs: [{cidr: 0.0.0.0/0, except: [10.0.0.0/8]}]
      toFQDNs: [{matchName: api.github.com, ports: [{port: 443, protocol: TCP}]}]
  scheduling:                 # see section 6
    nodeSelector: {mode: Force, labels: {location: site-a}}
    tolerations:  {mode: Allow, items: []}
status:
  observedGeneration: 3
  phase: Ready
  conditions: [Ready, NamespaceReady, QuotaReady, NetworkReady, SchedulingReady, ...]
  resolved: {...}             # profile + overrides after merge
```
### `TenantProfile` (cluster-scoped)
The equivalent of `values.yaml -> defaults`: quota, limit range, network baseline, PSA level, scheduling defaults. Admins edit one profile; the controller re-reconciles every Tenant that references it.

Merge order: profile -> Tenant overrides -> `status.resolved` (stored so you can see exactly what was applied). Maps merge, lists replace.

Versioning: start at `v1alpha1`. Add a storage version and conversion webhook when moving to `v1`.

## 4. Controller design
- One `TenantReconciler`. It watches `Tenant`, `TenantProfile` (mapped back to the Tenants that use it) and the owned/managed objects (Namespace, ResourceQuota, LimitRange, CiliumNetworkPolicy, Gatekeeper objects), filtered by a `managed-by` label.
- Feature modules implement one interface:
```go
type Feature interface {
    Name() string
    Enabled(t *ResolvedTenant) bool
    Reconcile(ctx context.Context, c client.Client, t *ResolvedTenant) error
    Cleanup(ctx context.Context, c client.Client, t *ResolvedTenant) error
}
```
- A registry runs modules in order: `namespace -> labels -> psa -> quota -> limitrange -> network -> scheduling(gatekeeper)`. A module returns an error or sets its own condition; one failed module does not block the others.
- **Server-side apply** with one field manager per module (`quarters/quota`, ...). That makes drift correction safe and avoids fighting other controllers.
- **Ownership:** namespaced children get an owner reference to the Namespace (so they are garbage-collected with it). The Namespace itself cannot be owned by a cluster-scoped CR through a normal reference in a Retain-friendly way, so it is tracked via a finalizer plus a `app.kubernetes.io/managed-by: quarters` label.
- **Deletion:** the finalizer runs each module's `Cleanup`. `Delete` removes the namespace, `Retain` removes the operator labels and leaves everything else.
- **Cilium typing:** use `unstructured` objects (or a generated typed client) so the operator does not pin to a Cilium Go module version. Check the CRD exists at startup and report a clear condition if not.
- Child cleanup of removed items: modules must delete objects they no longer desire (for example, switching from an allow policy to deny-all removes the old policy), found by label selector.

## 5. Admission
- **Validating webhook** on `Tenant`: name not reserved (`kube-*`, `default`), `profileRef` exists, quantities parse (catches `50h`), quota does not exceed profile ceiling, immutable fields, `scheduling` nodeSelector keys on an allowlist.
- Certificates through cert-manager.
- Gatekeeper is separate (section 6): the webhook protects the Tenant, Gatekeeper protects the pods.

## 6. Scheduling and node placement (new requirement)
Goal: per Tenant, either **force** all pods onto specific nodes or **allow** them to use certain nodes.

Your cluster has two node locations, so placement must be generic: nothing about "location" is hard-coded in Quarters. The node label key (for example `topology.kubernetes.io/zone` or your own `location`) and any taint are just data in the Tenant or the profile. Selectors and tolerations are configured **independently**, each with its own mode, so you can force one and merely allow the other.

```yaml
spec:
  scheduling:
    nodeSelector:
      mode: Force                  # Force | Allow | Disabled
      labels: {location: site-a}   # any key/value, not only location
    tolerations:
      mode: Allow                  # Force | Allow | Disabled
      items:
        - {key: dedicated, operator: Equal, value: site-a, effect: NoSchedule}
```
A `TenantProfile` can set defaults (for example `Allow` on both), and a Tenant overrides them.

| Mode | Behavior | Gatekeeper objects generated |
|---|---|---|
| `Force` | Every pod in the namespace gets the nodeSelector labels / tolerations injected, overriding a conflicting pod value | `Assign` mutation (`spec.nodeSelector` / `spec.tolerations`) scoped to the namespace |
| `Allow` | Pods may use the listed selectors / tolerations but are not forced. Values outside the allowed set are rejected | one `Constraint` of a shared template scoped to the namespace |
| `Disabled` | Nothing is injected or enforced | none (previously generated objects are deleted) |

Open question for the future: a `Deny` mode ("this tenant must never run on site-b") fits the same structure as a third mode and needs no redesign.

- To keep Gatekeeper "very simple" for now, the operator ships **one generic ConstraintTemplate** (installed with the operator, not per Tenant) and creates only small per-Tenant `Assign` and `Constraint` objects. The Gatekeeper module is the only code that knows about Gatekeeper.
- **Exclusive nodes** ("only this tenant on node X") needs two parts: a taint on the node (managed outside this operator) plus the `Force` toleration here. A later `Deny` rule (other tenants cannot tolerate that taint) is a natural second feature.
- Requires Gatekeeper mutation enabled (default in recent versions). Check at startup and set a `SchedulingReady=False` condition if not.
- Mutation applies on pod creation, so already-running pods are not changed until they restart. Document this.
- **Gatekeeper is not installed yet; install it hardened (maximum security profile):**
  - Install with the official Helm chart, pinned to a version, deployed by Argo CD before Quarters (sync wave).
  - Run 3 controller-manager/webhook replicas plus an audit pod, with a PodDisruptionBudget and topology spread across your two sites.
  - Set the validating **and** mutating webhook `failurePolicy` to `Fail`. The default `Ignore` fails open, which would let pods bypass `Force` placement whenever Gatekeeper is down. The price is that pod creation is blocked while Gatekeeper is unavailable, so HA is mandatory, not optional.
  - Exempt `kube-system` and `gatekeeper-system` (and any other infra namespaces) from the webhooks, so a Gatekeeper outage cannot deadlock the cluster or its own recovery.
  - Mutation must be enabled; Quarters checks for the `Assign` CRD at startup.
  - Check the exact Helm value names against the chart version you pin, as they change between releases.
  - Scope the webhook to only the namespaces managed by Quarters (label `app.kubernetes.io/managed-by: quarters`) to shrink the blast radius further.
- Alternative considered: the built-in `PodNodeSelector` admission plugin. It is simpler, but it is an API-server flag, often unavailable on managed clusters, and cannot express Allow mode, so Gatekeeper is the better fit here.

## 7. Migration from Helm
- Use the annotation `quarters.pinginfo.io/adopt: "true"` on a Tenant to take over an existing namespace and its objects instead of failing on "already exists".
- Steps: dry-run first (a status report of what would change), then SSA apply with force-conflicts only when adopting, then remove Helm labels and annotations (`app.kubernetes.io/managed-by: Helm`, `meta.helm.sh/*`) and finally uninstall the Helm release with the resource policy kept (or delete the release secret) so Helm does not delete anything.
- Do the namespaces one at a time, starting with `empty-test`.
- Existing object names must be preserved or migrated explicitly (`<ns>-quota`, `<ns>-quota-limit-range`, `<ns>-allow-ingress`, ...), otherwise duplicates appear.

## 8. Argo CD
- Argo only manages `Tenant` and `TenantProfile` CRs from Git. The children are created by the operator, so Argo never prunes them.
- Make sure the operator does **not** copy Argo's tracking label/annotation (`argocd.argoproj.io/tracking-id`, `app.kubernetes.io/instance`) from the Tenant onto children, otherwise Argo will treat them as orphans.
- Add a custom Argo health check that reads `status.conditions[type=Ready]`.
- Sync waves: operator and CRDs first, `TenantProfile`s next, `Tenant`s last.

## 9. Production readiness
- 2+ replicas, leader election, PodDisruptionBudget, topology spread, resource requests/limits.
- Hardened image: distroless, non-root, read-only root FS, drop all capabilities, seccomp RuntimeDefault.
- Least-privilege ClusterRole generated from kubebuilder markers: only namespaces, resourcequotas, limitranges, ciliumnetworkpolicies, Gatekeeper `Assign`/`Constraint`, events, leases.
- Metrics: reconcile duration, errors per module, drift corrections per module, number of tenants by phase. Alerts for reconcile failure rate and `Ready=False` for more than N minutes.
- Logs: structured, with `tenant` and `module` fields. Events on every state change.

## 10. Testing
- Unit tests per module with a fake client (merge logic, Cilium policy rendering golden files compared against the old Helm output).
- envtest for the reconciler (finalizer, deletion policy, drift correction).
- e2e on kind in CI: install Cilium CRDs (or a minimal stub), Gatekeeper, the operator, then apply sample Tenants. A migration test starts from the real `helm template` output.
- Parity test: render the old chart and the new operator for the sample namespaces and diff the resulting objects.

## 11. Repository layout
```
api/v1alpha1/            Tenant, TenantProfile types, validation markers
internal/controller/     TenantReconciler, profile mapping
internal/features/       one package per module: namespace, labels, psa, quota, limitrange, network, scheduling
internal/features/registry.go
internal/webhook/        validating webhook
internal/resolve/        profile + override merge
config/                  kustomize: crd, rbac, manager, webhook, samples
test/e2e/                kind-based tests
docs/                    ADRs
```

## 12. Build order
1. Scaffold (`kubebuilder init`, two APIs), types, CRD generation.
2. Resolve/merge logic with tests.
3. `namespace`, `labels`, `psa` modules.
4. `quota`, `limitrange` modules (fix the chart typos).
5. `network` module (Cilium) with golden-file parity tests.
6. Profiles and the validating webhook.
7. Adoption mode and the Helm migration runbook.
8. `scheduling` module with Gatekeeper (Force, then Allow).
9. Metrics, alerts, HA, hardened image, CI.

## 13. Open decisions
Decided: codename Quarters; K8s 1.36.x, Cilium 1.19.6, latest Gatekeeper; `team` is mandatory; scheduling selectors/tolerations are generic and independently configurable; default PSA follows the Talos default (see below).

PSA on Talos: Talos ships a default admission config that enforces `baseline` cluster-wide with exemptions for `kube-system`. The `default` profile therefore sets `pod-security.kubernetes.io/enforce: baseline` on the namespace (same as the cluster) plus `warn` and `audit: restricted` so teams see what would break. Stricter profiles can enforce `restricted` later.

Also decided: repo `quarters` and API group `quarters.pinginfo.io` confirmed; Gatekeeper will be installed fresh in the hardened configuration of section 6; node location label is `location` with values `site-a` and `site-b`.

Still to confirm:
- Are the nodes already tainted per site, and with which key?
- Dev tooling for the build: Go, kubebuilder, kind, docker, kubectl and helm are all missing from this machine.

## 14. Implementation notes (what was built, and where it differs from the plan above)
- Code layout follows section 11. Modules: Namespace, Quota, LimitRange, Network, Scheduling.
- Children (namespaced and cluster-scoped Gatekeeper objects) carry an owner reference to the Tenant, which
  Kubernetes allows. That replaces the label-only ownership idea in section 4. The Namespace has no owner
  reference; the finalizer and `deletionPolicy` decide its fate, and a namespace is only ever deleted if
  it carries the Quarters labels.
- Retain deletes nothing: the finalizer removes the owner references so garbage collection keeps the objects.
- `quota` is a map of ResourceQuota resource names to quantities, not a fixed struct. The webhook checks the
  keys and, for `count/<resource>.<group>`, asks the API server whether the resource exists. This catches
  typos such as `statefullstates`.
- Network selectors use namespace names and match on `io.kubernetes.pod.namespace`. The chart matched on
  `kubernetes.io/metadata.name` directly, which is a namespace label, not a pod label, so those rules
  probably never matched in Cilium. Verify against your cluster during migration.
- Tolerations in `Allow` mode are an exact allowlist (system `node.kubernetes.io/*` ones excepted).
  Forced tolerations need a key and are keyed on it, so two forced tolerations cannot share a key.
- Not done yet: a `Deny` scheduling mode, conversion webhooks (only v1alpha1 exists), PSA exemptions.
- Not tested against a real Gatekeeper or Cilium: envtest uses stub CRDs, so the Rego template and the
  exact shape of the `Assign` objects need a test on a cluster with Gatekeeper installed.
