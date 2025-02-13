package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/kyma-project/lifecycle-manager/api/v1alpha1"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	webhookTLSCfgNameTpl         = "%s-webhook-tls"
	SkrTLSName                   = "skr-webhook-tls"
	SkrResourceName              = "skr-webhook"
	IstioSystemNs                = "istio-system"
	IngressServiceName           = "istio-ingressgateway"
	defaultK3dLocalhostMapping   = "host.k3d.internal"
	defaultBufferSize            = 2048
	caCertKey                    = "ca.crt"
	tlsCertKey                   = "tls.crt"
	tlsPrivateKeyKey             = "tls.key"
	skrChartFieldOwner           = client.FieldOwner(v1alpha1.OperatorName)
	version                      = "v1"
	webhookTimeOutInSeconds      = 15
	allResourcesWebhookRule      = "*"
	statusSubResourceWebhookRule = "*/status"
)

var ErrLoadBalancerIPIsNotAssigned = errors.New("load balancer service external ip is not assigned")

type SkrWebhookManagerConfig struct {
	// SKRWatcherPath represents the path of the webhook resources
	// to be installed on SKR clusters upon reconciling kyma CRs.
	SKRWatcherPath         string
	SkrWebhookMemoryLimits string
	SkrWebhookCPULimits    string
	// WatcherLocalTestingEnabled indicates if the chart manager is running in local testing mode
	WatcherLocalTestingEnabled bool
	// GatewayHTTPPortMapping indicates the port used to expose the KCP cluster locally for the watcher callbacks
	GatewayHTTPPortMapping int
}

type WatchableConfig struct {
	Labels     map[string]string `json:"labels"`
	StatusOnly bool              `json:"statusOnly"`
}

func generateWatchableConfigs(watchers []v1alpha1.Watcher) map[string]WatchableConfig {
	chartCfg := make(map[string]WatchableConfig, 0)
	for _, watcher := range watchers {
		statusOnly := watcher.Spec.Field == v1alpha1.StatusField
		chartCfg[watcher.GetModuleName()] = WatchableConfig{
			Labels:     watcher.Spec.LabelsToWatch,
			StatusOnly: statusOnly,
		}
	}
	return chartCfg
}

type resourceOperation func(ctx context.Context, clt client.Client, resource client.Object) error

// runResourceOperationWithGroupedErrors loops through the resources and runs the passed operation
// on each resource concurrently and groups their returned errors into one.
func runResourceOperationWithGroupedErrors(ctx context.Context, clt client.Client,
	resources []client.Object, operation resourceOperation,
) error {
	errGrp, grpCtx := errgroup.WithContext(ctx)
	for idx := range resources {
		resIdx := idx
		errGrp.Go(func() error {
			return operation(grpCtx, clt, resources[resIdx])
		})
	}
	return errGrp.Wait()
}

func resolveKcpAddr(kcpConfig *rest.Config, managerConfig *SkrWebhookManagerConfig) (string, error) {
	if managerConfig.WatcherLocalTestingEnabled {
		return net.JoinHostPort(defaultK3dLocalhostMapping, strconv.Itoa(managerConfig.GatewayHTTPPortMapping)), nil
	}
	// Get public KCP IP from the ISTIO load balancer external IP
	kcpClient, err := client.New(kcpConfig, client.Options{})
	if err != nil {
		return "", err
	}
	ctx := context.TODO()
	loadBalancerService := &corev1.Service{}
	if err := kcpClient.Get(ctx, client.ObjectKey{
		Name:      IngressServiceName,
		Namespace: IstioSystemNs,
	}, loadBalancerService); err != nil {
		return "", err
	}
	if len(loadBalancerService.Status.LoadBalancer.Ingress) == 0 {
		return "", ErrLoadBalancerIPIsNotAssigned
	}
	externalIP := loadBalancerService.Status.LoadBalancer.Ingress[0].IP
	var port int32
	for _, loadBalancerPort := range loadBalancerService.Spec.Ports {
		if loadBalancerPort.Name == "http2" {
			port = loadBalancerPort.Port
			break
		}
	}
	return net.JoinHostPort(externalIP, strconv.Itoa(int(port))), nil
}

func resolveRemoteNamespace(kyma *v1alpha1.Kyma) string {
	if kyma.Spec.Sync.Namespace != "" {
		return kyma.Spec.Sync.Namespace
	}
	return kyma.Namespace
}

func ResolveTLSConfigSecretName(kymaName string) string {
	return fmt.Sprintf(webhookTLSCfgNameTpl, kymaName)
}

func getRawManifestUnstructuredResources(rawManifestReader io.Reader) ([]*unstructured.Unstructured, error) {
	decoder := yaml.NewYAMLOrJSONDecoder(rawManifestReader, defaultBufferSize)
	var resources []*unstructured.Unstructured
	for {
		resource := &unstructured.Unstructured{}
		err := decoder.Decode(resource)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if errors.Is(err, io.EOF) {
			break
		}
		resources = append(resources, resource)
	}
	return resources, nil
}
