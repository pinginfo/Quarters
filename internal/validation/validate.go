// Package validation holds the admission rules for Tenant and TenantProfile.
package validation

import (
	"fmt"
	"net"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sval "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
)

// reservedNames can never be a Tenant, as they are cluster infrastructure.
var reservedNames = map[string]bool{"default": true, "gatekeeper-system": true}

// reservedLabelPrefixes are set by the operator and cannot be set by a user.
var reservedLabelPrefixes = []string{"quarters.pinginfo.io/", "pod-security.kubernetes.io/"}

// reservedLabels are set by the operator and cannot be set by a user.
var reservedLabels = map[string]bool{"team": true, ManagedBy: true, "kubernetes.io/metadata.name": true}

// ManagedBy is the label the operator sets on everything it creates.
const ManagedBy = "app.kubernetes.io/managed-by"

// builtinQuotaKeys are the ResourceQuota names that are not count/<resource>.
var builtinQuotaKeys = map[string]bool{
	"pods": true, "services": true, "configmaps": true, "secrets": true,
	"persistentvolumeclaims": true, "replicationcontrollers": true, "resourcequotas": true,
	"services.nodeports": true, "services.loadbalancers": true,
	"cpu": true, "memory": true, "storage": true, "ephemeral-storage": true,
	"requests.cpu": true, "requests.memory": true, "requests.storage": true, "requests.ephemeral-storage": true,
	"limits.cpu": true, "limits.memory": true, "limits.ephemeral-storage": true,
}

// Validator validates Tenants. Mapper is optional; with it, quota keys of the
// form count/<resource>.<group> must name a resource the cluster knows, which
// catches typos that Kubernetes would accept silently.
type Validator struct {
	Mapper apimeta.RESTMapper
}

// Tenant validates a Tenant.
func (v Validator) Tenant(t *quartersv1alpha1.Tenant) field.ErrorList {
	var errs field.ErrorList
	meta := field.NewPath("metadata")
	spec := field.NewPath("spec")

	if msgs := k8sval.IsDNS1123Label(t.Name); len(msgs) > 0 {
		errs = append(errs, field.Invalid(meta.Child("name"), t.Name, strings.Join(msgs, "; ")))
	}
	if reservedNames[t.Name] || strings.HasPrefix(t.Name, "kube-") {
		errs = append(errs, field.Forbidden(meta.Child("name"), "reserved namespace name"))
	}
	if strings.TrimSpace(t.Spec.Team) == "" {
		errs = append(errs, field.Required(spec.Child("team"), "team is required"))
	} else if msgs := k8sval.IsValidLabelValue(t.Spec.Team); len(msgs) > 0 {
		errs = append(errs, field.Invalid(spec.Child("team"), t.Spec.Team, strings.Join(msgs, "; ")))
	}
	for k, val := range t.Spec.Labels {
		p := spec.Child("labels").Key(k)
		if reservedLabels[k] || hasPrefix(k, reservedLabelPrefixes) {
			errs = append(errs, field.Forbidden(p, "label is managed by the operator"))
		}
		for _, m := range k8sval.IsQualifiedName(k) {
			errs = append(errs, field.Invalid(p, k, m))
		}
		for _, m := range k8sval.IsValidLabelValue(val) {
			errs = append(errs, field.Invalid(p, val, m))
		}
	}
	for k := range t.Spec.Annotations {
		p := spec.Child("annotations").Key(k)
		if hasPrefix(k, reservedLabelPrefixes) {
			errs = append(errs, field.Forbidden(p, "annotation is managed by the operator"))
		}
		for _, m := range k8sval.IsQualifiedName(k) {
			errs = append(errs, field.Invalid(p, k, m))
		}
	}
	return append(errs, v.Config(spec, &t.Spec.TenantConfig)...)
}

// Profile validates a TenantProfile.
func (v Validator) Profile(p *quartersv1alpha1.TenantProfile) field.ErrorList {
	return v.Config(field.NewPath("spec"), &p.Spec.TenantConfig)
}

// Config validates the part shared by Tenant and TenantProfile.
func (v Validator) Config(path *field.Path, c *quartersv1alpha1.TenantConfig) field.ErrorList {
	var errs field.ErrorList
	for k, q := range c.Quota {
		p := path.Child("quota").Key(k)
		if q.Sign() < 0 {
			errs = append(errs, field.Invalid(p, q.String(), "must not be negative"))
		}
		if err := v.quotaKey(k); err != "" {
			errs = append(errs, field.Invalid(p, k, err))
		}
	}
	errs = append(errs, network(path.Child("network"), c.Network)...)
	errs = append(errs, scheduling(path.Child("scheduling"), c.Scheduling)...)
	return errs
}

func (v Validator) quotaKey(k string) string {
	if builtinQuotaKeys[k] {
		return ""
	}
	res, ok := strings.CutPrefix(k, "count/")
	if !ok {
		return "unknown quota resource; use a built-in name such as requests.cpu or count/<resource>[.<group>]"
	}
	if res == "" {
		return "count/ needs a resource name"
	}
	if v.Mapper == nil {
		return ""
	}
	gr := schema.ParseGroupResource(res)
	if _, err := v.Mapper.KindFor(gr.WithVersion("")); err != nil {
		return fmt.Sprintf("the cluster does not know the resource %q (check the spelling, e.g. statefulsets.apps)", res)
	}
	return ""
}

func network(p *field.Path, n *quartersv1alpha1.NetworkSpec) field.ErrorList {
	var errs field.ErrorList
	if n == nil {
		return nil
	}
	errs = append(errs, ingress(p.Child("ingress"), n.Ingress)...)
	errs = append(errs, egress(p.Child("egress"), n.Egress)...)
	seen := map[string]bool{}
	for i, pp := range n.PodPolicies {
		ppath := p.Child("podPolicies").Index(i)
		if msgs := k8sval.IsDNS1123Label(pp.Name); len(msgs) > 0 {
			errs = append(errs, field.Invalid(ppath.Child("name"), pp.Name, strings.Join(msgs, "; ")))
		} else if seen[pp.Name] {
			errs = append(errs, field.Duplicate(ppath.Child("name"), pp.Name))
		}
		seen[pp.Name] = true
		lp := ppath.Child("podSelector", "matchLabels")
		if len(pp.PodSelector.MatchLabels) != 1 {
			errs = append(errs, field.Invalid(lp, pp.PodSelector.MatchLabels, "select the pods by exactly one label"))
		}
		for k, v := range pp.PodSelector.MatchLabels {
			for _, m := range k8sval.IsQualifiedName(k) {
				errs = append(errs, field.Invalid(lp.Key(k), k, m))
			}
			for _, m := range k8sval.IsValidLabelValue(v) {
				errs = append(errs, field.Invalid(lp.Key(k), v, m))
			}
		}
		errs = append(errs, ingress(ppath.Child("ingress"), pp.Ingress)...)
		errs = append(errs, egress(ppath.Child("egress"), pp.Egress)...)
	}
	return errs
}

func nsList(path *field.Path, list []string) field.ErrorList {
	var errs field.ErrorList
	for i, ns := range list {
		if msgs := k8sval.IsDNS1123Label(ns); len(msgs) > 0 {
			errs = append(errs, field.Invalid(path.Index(i), ns, strings.Join(msgs, "; ")))
		}
	}
	return errs
}

func ingress(p *field.Path, in *quartersv1alpha1.IngressSpec) field.ErrorList {
	if in == nil {
		return nil
	}
	return nsList(p.Child("allowFromNamespaces"), in.AllowFromNamespaces)
}

func egress(p *field.Path, e *quartersv1alpha1.EgressSpec) field.ErrorList {
	if e == nil {
		return nil
	}
	errs := nsList(p.Child("toNamespaces"), e.ToNamespaces)
	for i, c := range e.ToCIDRs {
		cp := p.Child("toCIDRs").Index(i)
		if _, _, err := net.ParseCIDR(c.CIDR); err != nil {
			errs = append(errs, field.Invalid(cp.Child("cidr"), c.CIDR, "not a valid CIDR"))
		}
		for j, ex := range c.Except {
			if _, _, err := net.ParseCIDR(ex); err != nil {
				errs = append(errs, field.Invalid(cp.Child("except").Index(j), ex, "not a valid CIDR"))
			}
		}
	}
	for i, f := range e.ToFQDNs {
		fp := p.Child("toFQDNs").Index(i)
		if (f.MatchName == "") == (f.MatchPattern == "") {
			errs = append(errs, field.Invalid(fp, f.MatchName+f.MatchPattern, "set exactly one of matchName and matchPattern"))
		}
		if strings.ContainsAny(f.MatchName, "*?") {
			errs = append(errs, field.Invalid(fp.Child("matchName"), f.MatchName, "wildcards are not allowed in matchName, use matchPattern"))
		}
	}
	return errs
}

func scheduling(p *field.Path, s *quartersv1alpha1.SchedulingSpec) field.ErrorList {
	var errs field.ErrorList
	if s == nil {
		return nil
	}
	if ns := s.NodeSelector; ns != nil {
		np := p.Child("nodeSelector")
		if active(ns.Mode) && len(ns.Labels) == 0 {
			errs = append(errs, field.Required(np.Child("labels"), "at least one label is needed in mode "+string(ns.Mode)))
		}
		for k, val := range ns.Labels {
			for _, m := range k8sval.IsQualifiedName(k) {
				errs = append(errs, field.Invalid(np.Child("labels").Key(k), k, m))
			}
			for _, m := range k8sval.IsValidLabelValue(val) {
				errs = append(errs, field.Invalid(np.Child("labels").Key(k), val, m))
			}
			if strings.ContainsAny(k, `"\`) || strings.ContainsAny(val, `"\`) {
				errs = append(errs, field.Invalid(np.Child("labels").Key(k), k, `quotes and backslashes are not supported`))
			}
		}
	}
	if t := s.Tolerations; t != nil {
		tp := p.Child("tolerations")
		if active(t.Mode) && len(t.Items) == 0 && t.Mode == quartersv1alpha1.SchedulingForce {
			errs = append(errs, field.Required(tp.Child("items"), "at least one toleration is needed in mode Force"))
		}
		seen := map[string]bool{}
		for i, it := range t.Items {
			ip := tp.Child("items").Index(i)
			if t.Mode == quartersv1alpha1.SchedulingForce {
				if it.Key == "" {
					errs = append(errs, field.Required(ip.Child("key"), "a forced toleration needs a key"))
				}
				if seen[it.Key] {
					errs = append(errs, field.Duplicate(ip.Child("key"), it.Key))
				}
				seen[it.Key] = true
			}
			if strings.ContainsAny(it.Key, `"\`) {
				errs = append(errs, field.Invalid(ip.Child("key"), it.Key, `quotes and backslashes are not supported`))
			}
		}
	}
	return errs
}

func active(m quartersv1alpha1.SchedulingMode) bool {
	return m == quartersv1alpha1.SchedulingForce || m == quartersv1alpha1.SchedulingAllow
}

func hasPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
