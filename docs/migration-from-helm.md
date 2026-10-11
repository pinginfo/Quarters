# Migrating a namespace from the `ns-config` Helm chart

Quarters can take over a namespace the chart created, without recreating anything.
Do one namespace at a time, starting with the least important (`empty-test`).

## Before you start
1. Install Quarters, the `default` profile and (for Allow scheduling) the Gatekeeper template.
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
