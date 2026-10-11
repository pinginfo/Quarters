package controller

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	quartersv1alpha1 "github.com/pinginfo/quarters/api/v1alpha1"
	"github.com/pinginfo/quarters/internal/validation"
)

// The shipped samples must be valid against the CRD schema, pass the admission
// rules and reconcile to Ready.
var _ = Describe("Sample manifests", func() {
	It("apply and reconcile to Ready", func() {
		var tenants []string
		for _, file := range []string{"profile-default.yaml", "tenants.yaml", "tenant-scheduling.yaml"} {
			raw, err := os.ReadFile(filepath.Join("..", "..", "config", "samples", file))
			Expect(err).NotTo(HaveOccurred())

			dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
			for {
				u := &unstructured.Unstructured{}
				err := dec.Decode(&u.Object)
				if errors.Is(err, io.EOF) {
					break
				}
				Expect(err).NotTo(HaveOccurred())
				if len(u.Object) == 0 {
					continue
				}
				Expect(k8sClient.Create(ctx, u)).To(Succeed(), "%s %s", file, u.GetName())
				if u.GetKind() == "Tenant" {
					tenants = append(tenants, u.GetName())
				}
			}
		}
		Expect(tenants).To(HaveLen(5))

		r := &TenantReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: record.NewFakeRecorder(1000)}
		for _, name := range tenants {
			t := &quartersv1alpha1.Tenant{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name}, t)).To(Succeed())
			Expect(validation.Validator{}.Tenant(t)).To(BeEmpty(), name)

			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: name}})
			Expect(err).NotTo(HaveOccurred(), name)
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name}, t)).To(Succeed())
			Expect(t.Status.Phase).To(Equal("Ready"), name)
		}
	})
})
