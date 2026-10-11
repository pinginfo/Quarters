package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
	"github.com/pinginfo/quarters/internal/validation"
)

var _ = Describe("TenantProfile Webhook", func() {
	validator := TenantProfileValidator{Rules: validation.Validator{}}

	profile := func(quota map[string]resource.Quantity) *quartersv1alpha1.TenantProfile {
		return &quartersv1alpha1.TenantProfile{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Spec:       quartersv1alpha1.TenantProfileSpec{TenantConfig: quartersv1alpha1.TenantConfig{Quota: quota}},
		}
	}

	It("admits a valid profile", func() {
		p := profile(map[string]resource.Quantity{"pods": resource.MustParse("20")})
		Expect(validator.ValidateCreate(ctx, p)).Error().NotTo(HaveOccurred())
	})

	It("denies an unknown quota key", func() {
		p := profile(map[string]resource.Quantity{"podz": resource.MustParse("20")})
		Expect(validator.ValidateCreate(ctx, p)).Error().To(HaveOccurred())
	})
})
