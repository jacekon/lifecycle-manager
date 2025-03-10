package kcp_test

import (
	"context"
	"errors"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	compdescv2 "ocm.software/ocm/api/ocm/compdesc/versions/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kyma-project/lifecycle-manager/api/shared"
	"github.com/kyma-project/lifecycle-manager/api/v1beta2"
	"github.com/kyma-project/lifecycle-manager/pkg/testutils/builder"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/kyma-project/lifecycle-manager/pkg/testutils"
)

var (
	ErrExpectedLabelNotReset    = errors.New("expected label not reset")
	ErrWatcherLabelMissing      = errors.New("watcher label missing")
	ErrWatcherAnnotationMissing = errors.New("watcher annotation missing")
	ErrGlobalChannelMisMatch    = errors.New("kyma global channel mismatch")
)

const (
	InitSpecKey   = "initKey"
	InitSpecValue = "initValue"
)

func registerControlPlaneLifecycleForKyma(kyma *v1beta2.Kyma) {
	BeforeAll(func() {
		DeployModuleTemplates(ctx, kcpClient, kyma)
		Eventually(CreateCR, Timeout, Interval).
			WithContext(ctx).
			WithArguments(kcpClient, kyma).Should(Succeed())
	})

	AfterAll(func() {
		Eventually(DeleteCR, Timeout, Interval).
			WithContext(ctx).
			WithArguments(kcpClient, kyma).Should(Succeed())
		DeleteModuleTemplates(ctx, kcpClient, kyma)
	})

	BeforeEach(func() {
		By("get latest kyma CR")
		Eventually(SyncKyma, Timeout, Interval).
			WithContext(ctx).WithArguments(kcpClient, kyma).Should(Succeed())
	})
}

func DeleteModuleTemplates(ctx context.Context, kcpClient client.Client, kyma *v1beta2.Kyma) {
	for _, module := range kyma.Spec.Modules {
		template := builder.NewModuleTemplateBuilder().
			WithNamespace(ControlPlaneNamespace).
			WithLabelModuleName(module.Name).
			WithChannel(module.Channel).
			WithOCM(compdescv2.SchemaVersion).Build()
		Eventually(DeleteCR, Timeout, Interval).
			WithContext(ctx).
			WithArguments(kcpClient, template).Should(Succeed())
	}
}

func DeployModuleTemplates(ctx context.Context, kcpClient client.Client, kyma *v1beta2.Kyma) {
	for _, module := range kyma.Spec.Modules {
		template := builder.NewModuleTemplateBuilder().
			WithNamespace(ControlPlaneNamespace).
			WithLabelModuleName(module.Name).
			WithChannel(module.Channel).
			WithOCM(compdescv2.SchemaVersion).Build()
		Eventually(kcpClient.Create, Timeout, Interval).WithContext(ctx).
			WithArguments(template).
			Should(Succeed())
	}
}

func kymaChannelMatch(clnt client.Client, name, namespace, channel string) error {
	kyma, err := GetKyma(ctx, clnt, name, namespace)
	if err != nil {
		return err
	}
	if kyma.Spec.Channel != channel {
		return ErrGlobalChannelMisMatch
	}
	return nil
}

func watcherLabelsAnnotationsExist(clnt client.Client, remoteKyma *v1beta2.Kyma, kyma *v1beta2.Kyma,
	remoteSyncNamespace string,
) error {
	remoteKyma, err := GetKyma(ctx, clnt, remoteKyma.GetName(), remoteSyncNamespace)
	if err != nil {
		return err
	}
	if remoteKyma.Labels[shared.WatchedByLabel] != shared.WatchedByLabelValue {
		return ErrWatcherLabelMissing
	}
	if remoteKyma.Annotations[shared.OwnedByAnnotation] != fmt.Sprintf(shared.OwnedByFormat,
		kyma.GetNamespace(), kyma.GetName()) {
		return ErrWatcherAnnotationMissing
	}
	return nil
}

func expectModuleTemplateSpecGetReset(
	clnt client.Client,
	module v1beta2.Module,
	kyma *v1beta2.Kyma,
) error {
	moduleTemplate, err := GetModuleTemplate(ctx, clnt, module, kyma)
	if err != nil {
		return err
	}
	if moduleTemplate.Spec.Data == nil {
		return ErrManifestResourceIsNil
	}
	initKey, found := moduleTemplate.Spec.Data.Object["spec"]
	if !found {
		return ErrExpectedLabelNotReset
	}
	initKeyM, mapOk := initKey.(map[string]any)
	if !mapOk {
		return ErrExpectedLabelNotReset
	}
	value, found := initKeyM[InitSpecKey]
	if !found {
		return ErrExpectedLabelNotReset
	}
	sValue, ok := value.(string)
	if !ok {
		return ErrExpectedLabelNotReset
	}
	if sValue != InitSpecValue {
		return ErrExpectedLabelNotReset
	}
	return nil
}

func kymaHasCondition(
	clnt client.Client,
	conditionType v1beta2.KymaConditionType,
	reason string,
	status apimetav1.ConditionStatus,
	kymaName,
	kymaNamespace string,
) error {
	kyma, err := GetKyma(ctx, clnt, kymaName, kymaNamespace)
	if err != nil {
		return err
	}

	for _, cnd := range kyma.Status.Conditions {
		if cnd.Type == string(conditionType) && cnd.Reason == reason && cnd.Status == status {
			return nil
		}
	}

	return ErrNotContainsExpectedCondition
}

func containsModuleTemplateCondition(clnt client.Client, kymaName, kymaNamespace string) error {
	kyma, err := GetKyma(ctx, clnt, kymaName, kymaNamespace)
	if err != nil {
		return err
	}
	if !kyma.ContainsCondition(v1beta2.ConditionTypeModuleCatalog) {
		return ErrNotContainsExpectedCondition
	}
	return nil
}

func updateKymaCRD(clnt client.Client) (*apiextensionsv1.CustomResourceDefinition, error) {
	return updateCRDPropertyDescription(clnt, shared.KymaKind, "channel", "test change")
}

func updateModuleReleaseMetaCRD(clnt client.Client) (*apiextensionsv1.CustomResourceDefinition, error) {
	return updateCRDPropertyDescription(clnt, shared.ModuleReleaseMetaKind, "channels", "test change")
}

func updateCRDPropertyDescription(clnt client.Client, crdKind shared.Kind,
	propertyName, newValue string,
) (*apiextensionsv1.CustomResourceDefinition, error) {
	crd, err := fetchCrd(clnt, crdKind)
	if err != nil {
		return nil, err
	}

	crd.SetManagedFields(nil)
	crdSpecVersions := crd.Spec.Versions
	channelProperty := getCrdSpec(crd).Properties[propertyName]
	channelProperty.Description = newValue
	getCrdSpec(crd).Properties[propertyName] = channelProperty
	crd.Spec = apiextensionsv1.CustomResourceDefinitionSpec{
		Versions:              crdSpecVersions,
		Names:                 crd.Spec.Names,
		Group:                 crd.Spec.Group,
		Conversion:            crd.Spec.Conversion,
		Scope:                 crd.Spec.Scope,
		PreserveUnknownFields: crd.Spec.PreserveUnknownFields,
	}
	if err := clnt.Patch(ctx, crd,
		client.Apply,
		client.ForceOwnership,
		client.FieldOwner(shared.OperatorName)); err != nil {
		return nil, err
	}
	crd, err = fetchCrd(clnt, crdKind)
	crdName := fmt.Sprintf("%s.%s", crdKind.Plural(), v1beta2.GroupVersion.Group)

	// Replace the cached CRD after updating the KCP CRD to validate that
	// the Generation values are updated correctly
	if _, ok := crdCache.Get(crdName); ok {
		crdCache.Add(crdName, *crd)
	}
	if err != nil {
		return nil, err
	}
	return crd, nil
}

func getCrdSpec(crd *apiextensionsv1.CustomResourceDefinition) apiextensionsv1.JSONSchemaProps {
	return crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
}

func fetchCrd(clnt client.Client, crdKind shared.Kind) (*apiextensionsv1.CustomResourceDefinition, error) {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := clnt.Get(
		ctx, client.ObjectKey{
			Name: fmt.Sprintf("%s.%s", crdKind.Plural(), v1beta2.GroupVersion.Group),
		}, crd,
	); err != nil {
		return nil, err
	}

	return crd, nil
}
