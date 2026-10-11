# Migrating a namespace from the `ns-config` Helm chart

`ns-config` is an internal Helm chart, not a public one. It created each namespace
from a block in its `values.yaml`, with shared `defaults` and per-namespace overrides.

## What `ns-config` was

One Helm release for the whole cluster. Its `values.yaml` has a `defaults` block and a
`namespaces` list, and every template loops over that list with `range .Values.namespaces`,
so each entry produced a complete set of objects.

```yaml
defaults:                      # shared quota, merged under every namespace
  quota:
    requests: {cpu: "2", memory: "4Gi", storage: "20Gi"}
    limits:   {cpu: "4", memory: "8Gi"}
    pods: "20"
    statefulsets: "5"
    # ...

namespaces:
  - name: app-production       # the namespace name
    labels: {team: backend, environment: production}
    annotations: {owner: backend-team@example.com}
    quota:                     # overrides `defaults.quota` key by key
      requests: {cpu: "16", memory: "32Gi"}
      pods: "50"
    ingressPolicy:             # absent => deny all ingress
      allowIngressFromSameNamespace: true
      allowIngressFromNamespaces:
        - kubernetes.io/metadata.name: ingress-nginx
    egressPolicy:              # nothing listed => deny all egress
      allowEgressToNamespaces:
        - kubernetes.io/metadata.name: kube-system
      allowEgressToCIDR:
        - cidr: "0.0.0.0/0"
          except: ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"]
      allowEgressToFQDNs:
        - matchName: "api.github.com"
          ports: [{port: "443", protocol: "TCP"}]
```

| Template | Objects per namespace | What it did |
|---|---|---|
| `namespace.yaml` | `Namespace` | Name, `labels` and `annotations` from the entry, plus `app.kubernetes.io/managed-by: Helm`. |
| `quota.yaml` | `ResourceQuota` `<ns>-quota`, `LimitRange` `<ns>-quota-limit-range` | Quota is `defaults.quota` deep-merged with the entry's `quota` (`mergeOverwrite`), then each known key is written if set. The LimitRange is hard-coded (100m CPU, 128Mi memory) and cannot be changed per namespace. |
| `ciliumingresspolicy.yaml` | `CiliumNetworkPolicy` `<ns>-allow-ingress`, or `<ns>-deny-ingress` | No `ingressPolicy`: an `ingressDeny` from all entities. Otherwise an allow policy built from `allowIngressFromSameNamespace`, `allowCiliumIngress` (the `ingress` entity) and `allowIngressFromNamespaces`. |
| `ciliumegresspolicy.yaml` | `CiliumNetworkPolicy` `<ns>-allow-egress`, or `<ns>-deny-egress` | Nothing listed: an `egressDeny` of everything. Otherwise DNS to kube-dns, then one rule per namespace, CIDR (with `except`) and FQDN. |

Limits that led to Quarters:

- Only the quota is defaulted. Labels, policies and the LimitRange have no shared defaults.
- One release for every namespace: changing one entry re-renders them all, and a bad value breaks the whole release.
- No validation, so typos went through silently: `secrets: "50h"` is not a quantity, and the quota
  keys `count/deployments.app` and `count/statefullstates.apps` are misspelled, so they never limited anything.
- `allowAllEgress` appears in `values.yaml` but no template reads it.
- No status, no deletion handling, no scheduling or Pod Security settings.

## How it maps to Quarters

| `ns-config` | Quarters |
|---|---|
| `defaults:` block | `TenantProfile` (`config/samples/profile-default.yaml`) |
| an entry of `namespaces:` | one `Tenant` (`config/samples/tenants.yaml`) |
| `name` | `metadata.name` of the Tenant |
| `labels`, `annotations` | `spec.labels`, `spec.annotations` (`team` becomes the mandatory `spec.team`) |
| `quota.requests.cpu`, `quota.pods`, `quota.statefulsets` | `spec.quota` keyed by resource name: `requests.cpu`, `pods`, `count/statefulsets.apps` |
| hard-coded LimitRange | `spec.limitRange` |
| `ingressPolicy.allowIngressFromSameNamespace`, `allowCiliumIngress`, `allowIngressFromNamespaces` | `spec.network.ingress.allowSameNamespace`, `allowCiliumIngress`, `allowFromNamespaces` (namespace names, not label maps) |
| `egressPolicy.allowEgressToNamespaces`, `ToCIDR`, `ToFQDNs` | `spec.network.egress.toNamespaces`, `toCIDRs`, `toFQDNs`; `allowAll` now works |
| `helm template` output | the objects Quarters applies, with the same names |

Quarters can take over a namespace the chart created, without recreating anything.
Do one namespace at a time, starting with the least important (`empty-test`).

## Before you start
1. Install Quarters, the `default` profile and (for scheduling) the Gatekeeper template, from GHCR as described in the [README](../README.md#install).
2. Translate the namespace's block of `values.yaml` into a `Tenant` (see `config/samples/tenants.yaml`).
   Fix the known chart problems on the way: `count/statefulsets.apps`, `secrets: "50h"`,
   and review `0.0.0.0/0` egress rules.
3. Keep the names. Quarters uses the chart's names (`<ns>-quota`, `<ns>-quota-limit-range`,
   `<ns>-allow-ingress`, ...), so existing objects are updated in place, not duplicated.

## Adopt
1. Put the Tenant in Git with the annotation `quarters.pinginfo.io/adopt: "true"`.
   Without it Quarters refuses to touch an existing namespace it did not create
   (condition `NamespaceReady=False`).
2. Let Argo CD sync it. Quarters applies its configuration over the existing objects
   and removes the `meta.helm.sh/*` annotations from them.
3. Check `kubectl get tenant <name>` is `Ready`, and compare the objects with the chart output
   (`helm template` vs `kubectl get -o yaml`). Differences are your intended changes.

## Release it from Helm without deleting anything
`helm uninstall` would delete the objects. Instead remove Helm's release records only:

```sh
kubectl -n <release-namespace> get secret -l owner=helm,name=<release-name>
kubectl -n <release-namespace> delete secret -l owner=helm,name=<release-name>
```

Do this only after **every** namespace of the chart is adopted, as the chart is one release
for all namespaces. Alternatively remove one namespace at a time from `values.yaml` and
`helm upgrade` with the resources kept (`helm.sh/resource-policy: keep` on the objects first).

## Afterwards
- Remove the adopt annotation (harmless to keep, it only matters when the namespace is not yet managed).
- Argo CD manages only the `Tenant` and `TenantProfile` objects, never the children, so it cannot prune them.

## Roll back
Delete the Tenant with `deletionPolicy: Retain` (the default). The namespace and objects stay;
re-install the chart release to go back.
