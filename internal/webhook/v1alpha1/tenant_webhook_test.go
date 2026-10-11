package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
	"github.com/pinginfo/quarters/internal/validation"
)

var _ = Describe("Tenant Webhook", func() {
	var (
		validator TenantValidator
		tenant    *quartersv1alpha1.Tenant
	)

	BeforeEach(func() {
		validator = TenantValidator{Rules: validation.Validator{}}
		tenant = &quartersv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{Name: "app-production"},
			Spec:       quartersv1alpha1.TenantSpec{Team: "backend"},
		}
	})

	It("admits a valid Tenant", func() {
		Expect(validator.ValidateCreate(ctx, tenant)).Error().NotTo(HaveOccurred())
		Expect(validator.ValidateUpdate(ctx, tenant, tenant)).Error().NotTo(HaveOccurred())
	})

	It("denies a Tenant without team", func() {
		tenant.Spec.Team = ""
		Expect(validator.ValidateCreate(ctx, tenant)).Error().To(MatchError(ContainSubstring("team")))
	})

	It("denies a reserved namespace name", func() {
		tenant.Name = "kube-system"
		Expect(validator.ValidateCreate(ctx, tenant)).Error().To(HaveOccurred())
	})

	It("always admits updates of a Tenant being deleted, so its finalizer can be removed", func() {
		tenant.Spec.Team = ""
		now := metav1.Now()
		tenant.DeletionTimestamp = &now
		Expect(validator.ValidateUpdate(ctx, tenant, tenant)).Error().NotTo(HaveOccurred())
	})
})
