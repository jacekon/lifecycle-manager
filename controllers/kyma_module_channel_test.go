package controllers_test

import (
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kyma-project/lifecycle-manager/api/v1alpha1"
	. "github.com/kyma-project/lifecycle-manager/pkg/testutils"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	FastChannel             = "fast"
	ValidChannel            = "valid"
	InValidChannel          = "Invalid01"                                       // lower case characters from a to z
	InValidMinLengthChannel = "ch"                                              // minlength = 3
	InValidMaxLengthChannel = "averylongchannelwhichlargerthanallowedmaxlength" // maxlength = 32
	LowerVersion            = "0.0.1"
	HigherVersion           = "0.0.2"
)

var _ = Describe("valid channel should be deployed successful", func() {
	kyma := NewTestKyma("kyma")
	It("should create kyma with standard modules in a valid channel", func() {
		kyma.Spec.Channel = ValidChannel
		Expect(controlPlaneClient.Create(ctx, kyma)).ToNot(HaveOccurred())
	})
	DescribeTable(
		"Test Channel Status", func(givenCondition func() error, expectedBehavior func() error) {
			Eventually(givenCondition, Timeout, Interval).Should(Succeed())
			Eventually(expectedBehavior, Timeout, Interval).Should(Succeed())
		},
		Entry(
			"When kyma is deployed in valid channel,"+
				" expect Modules to be in valid channel",
			givenModuleTemplateWithChannel(ValidChannel, true),
			expectEveryModuleStatusToHaveChannel(kyma.Name, ValidChannel),
		),
	)
})

var _ = Describe("Given invalid channel", func() {
	DescribeTable(
		"Test kyma CR, module template creation", func(givenCondition func() error) {
			Eventually(givenCondition, Timeout, Interval).Should(Succeed())
		},
		Entry(
			"invalid channel with not allowed characters",
			givenModuleTemplateWithChannel(InValidChannel, false),
		),
		Entry(
			"invalid channel with less than min length",
			givenModuleTemplateWithChannel(InValidMinLengthChannel, false),
		),
		Entry(
			"invalid channel with more than max length",
			givenModuleTemplateWithChannel(InValidMaxLengthChannel, false),
		),
		Entry(
			"invalid channel with not allowed characters",
			givenKymaWithInvalidChannel(InValidChannel),
		),
		Entry(
			"invalid channel with less than min length",
			givenKymaWithInvalidChannel(InValidMinLengthChannel),
		),
		Entry(
			"invalid channel with more than max length",
			givenKymaWithInvalidChannel(InValidMaxLengthChannel),
		),
		Entry(
			"invalid channel with not allowed characters",
			givenKymaSpecModulesWithInvalidChannel(InValidChannel),
		),
		Entry(
			"invalid channel with less than min length",
			givenKymaSpecModulesWithInvalidChannel(InValidMinLengthChannel),
		),
		Entry(
			"invalid channel with more than max length",
			givenKymaSpecModulesWithInvalidChannel(InValidMaxLengthChannel),
		),
	)
})

func givenModuleTemplateWithChannel(channel string, isValid bool) func() error {
	return func() error {
		modules := []v1alpha1.Module{
			{
				ControllerName: "manifest",
				Name:           "module-with-" + channel,
				Channel:        channel,
			},
		}
		err := CreateModuleTemplateSetsForKyma(modules, LowerVersion, channel)
		if isValid {
			return err
		}
		return ignoreInvalidError(err)
	}
}

func givenKymaWithInvalidChannel(channel string) func() error {
	return func() error {
		kyma := NewTestKyma("kyma")
		kyma.Spec.Channel = channel
		err := controlPlaneClient.Create(ctx, kyma)
		return ignoreInvalidError(err)
	}
}

func ignoreInvalidError(err error) error {
	var statusError *apiErrors.StatusError
	ok := errors.As(err, &statusError)
	Expect(ok).Should(BeTrue())
	if statusError.ErrStatus.Reason != metaV1.StatusReasonInvalid {
		return fmt.Errorf("status error not match: expect %s, actual %w", metaV1.StatusReasonInvalid, err)
	}
	return nil
}

func givenKymaSpecModulesWithInvalidChannel(channel string) func() error {
	return func() error {
		kyma := NewTestKyma("kyma")
		kyma.Spec.Modules = append(
			kyma.Spec.Modules, v1alpha1.Module{
				ControllerName: "manifest",
				Name:           "module-with-" + channel,
				Channel:        channel,
			})
		err := controlPlaneClient.Create(ctx, kyma)
		return ignoreInvalidError(err)
	}
}

var _ = Describe("Switching of a Channel with higher version leading to an Upgrade", Ordered, func() {
	kyma := NewTestKyma("empty-module-kyma")

	kyma.Spec.Modules = append(
		kyma.Spec.Modules, v1alpha1.Module{
			ControllerName: "manifest",
			Name:           "channel-switch",
			Channel:        v1alpha1.DefaultChannel,
		})

	AfterAll(func() {
		Expect(controlPlaneClient.Delete(ctx, kyma)).Should(Succeed())
	})

	BeforeAll(func() {
		Expect(CreateModuleTemplateSetsForKyma(kyma.Spec.Modules, LowerVersion, v1alpha1.DefaultChannel)).To(Succeed())
		Expect(CreateModuleTemplateSetsForKyma(kyma.Spec.Modules, HigherVersion, FastChannel)).To(Succeed())
	})

	AfterAll(CleanupModuleTemplateSetsForKyma(kyma))

	It(
		"should create kyma with standard modules in default channel normally", func() {
			Expect(controlPlaneClient.Create(ctx, kyma)).ToNot(HaveOccurred())
			Eventually(GetKymaState(kyma.Name), 5*time.Second, Interval).
				Should(BeEquivalentTo(string(v1alpha1.StateProcessing)))
			for _, module := range kyma.Spec.Modules {
				Eventually(
					UpdateModuleState(ctx, kyma, module, v1alpha1.StateReady), 20*time.Second,
					Interval).Should(Succeed())
			}
			Eventually(GetKymaState(kyma.Name), 5*time.Second, Interval).
				Should(BeEquivalentTo(string(v1alpha1.StateReady)))
		},
	)

	DescribeTable(
		"Test Channel Status", func(givenCondition func() error, expectedBehavior func() error) {
			Eventually(givenCondition, Timeout, Interval).Should(Succeed())
			Eventually(expectedBehavior, Timeout, Interval).Should(Succeed())
		},
		Entry(
			"When kyma is deployed in default channel with lower version,"+
				" expect Modules to be in regular channel",
			noCondition(),
			expectEveryModuleStatusToHaveChannel(kyma.Name, v1alpha1.DefaultChannel),
		),
		Entry(
			"When all modules are updated to fast channel with higher version,"+
				" expect Modules to update to fast channel",
			whenUpdatingEveryModuleChannel(kyma.Name, FastChannel),
			expectEveryModuleStatusToHaveChannel(kyma.Name, FastChannel),
		),
		Entry(
			"When all modules are reverted to regular channel,"+
				" expect Modules to stay in fast channel",
			whenUpdatingEveryModuleChannel(kyma.Name, v1alpha1.DefaultChannel),
			expectEveryModuleStatusToHaveChannel(kyma.Name, FastChannel),
		),
	)

	It(
		"should lead to kyma being ready in the end of the channel switch", func() {
			By("having updated the Kyma CR state to ready")
			Eventually(GetKymaState(kyma.Name), 20*time.Second, Timeout).
				Should(BeEquivalentTo(string(v1alpha1.StateReady)))
		},
	)
},
)

func CleanupModuleTemplateSetsForKyma(kyma *v1alpha1.Kyma) func() {
	return func() {
		By("Cleaning up decremented ModuleTemplate set in regular")
		for _, module := range kyma.Spec.Modules {
			template, err := ModuleTemplateFactory(module, unstructured.Unstructured{})
			template.Name = fmt.Sprintf("%s-%s", template.Name, v1alpha1.DefaultChannel)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(controlPlaneClient.Delete(ctx, template)).To(Succeed())
		}
		By("Cleaning up standard ModuleTemplate set in fast")
		for _, module := range kyma.Spec.Modules {
			template, err := ModuleTemplateFactory(module, unstructured.Unstructured{})
			template.Name = fmt.Sprintf("%s-%s", template.Name, FastChannel)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(controlPlaneClient.Delete(ctx, template)).To(Succeed())
		}
	}
}

func expectEveryModuleStatusToHaveChannel(kymaName, channel string) func() error {
	return func() error {
		return TemplateInfosMatchChannel(kymaName, channel)
	}
}

func whenUpdatingEveryModuleChannel(kymaName, channel string) func() error {
	return func() error {
		return UpdateKymaModuleChannels(kymaName, channel)
	}
}
