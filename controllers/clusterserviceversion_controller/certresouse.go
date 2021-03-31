package controllers

import (
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/buptGophers/operator-manager/api/v1alpha1"
)

var _ certResource = &apiServiceDescriptionsWithCAPEM{}

var _ certResource = &webhookDescriptionWithCAPEM{}

// TODO: to keep refactoring minimal for backports, this is factored out here so that it can be replaced
// during tests. but it should be properly injected instead.
var certGenerator CertGenerator = CertGeneratorFunc(CreateSignedServingPair)

const (
	// DefaultCertMinFresh is the default min-fresh value - 1 day
	DefaultCertMinFresh = time.Hour * 24
	// DefaultCertValidFor is the default duration a cert can be valid for - 2 years
	DefaultCertValidFor = time.Hour * 24 * 730
	// OLMCAPEMKey is the CAPEM
	OLMCAPEMKey = "olmCAKey"
	// OLMCAHashAnnotationKey is the label key used to store the hash of the CA cert
	OLMCAHashAnnotationKey = "olmcahash"
	// Organization is the organization name used in the generation of x509 certs
	Organization = "Red Hat, Inc."
	// Kubernetes System namespace
	KubeSystem = "kube-system"
)

type certResource interface {
	getName() string
	setCAPEM(caPEM []byte)
	getCAPEM() []byte
	getServicePort() corev1.ServicePort
	getDeploymentName() string
}

func containsServicePort(servicePorts []corev1.ServicePort, targetServicePort corev1.ServicePort) bool {
	for _, servicePort := range servicePorts {
		if servicePort == targetServicePort {
			return true
		}
	}

	return false
}

type apiServiceDescriptionsWithCAPEM struct {
	apiServiceDescription v1alpha1.APIServiceDescription
	caPEM                 []byte
}

func (i *apiServiceDescriptionsWithCAPEM) getName() string {
	return i.apiServiceDescription.Name
}

func (i *apiServiceDescriptionsWithCAPEM) setCAPEM(caPEM []byte) {
	i.caPEM = caPEM
}

func (i *apiServiceDescriptionsWithCAPEM) getCAPEM() []byte {
	return i.caPEM
}

func (i *apiServiceDescriptionsWithCAPEM) getDeploymentName() string {
	return i.apiServiceDescription.DeploymentName
}

func (i *apiServiceDescriptionsWithCAPEM) getServicePort() corev1.ServicePort {
	containerPort := 443
	if i.apiServiceDescription.ContainerPort > 0 {
		containerPort = int(i.apiServiceDescription.ContainerPort)
	}
	return corev1.ServicePort{
		Name:       strconv.Itoa(containerPort),
		Port:       int32(containerPort),
		TargetPort: intstr.FromInt(containerPort),
	}
}

type webhookDescriptionWithCAPEM struct {
	webhookDescription v1alpha1.WebhookDescription
	caPEM              []byte
}

func (i *webhookDescriptionWithCAPEM) getName() string {
	return i.webhookDescription.GenerateName
}

func (i *webhookDescriptionWithCAPEM) setCAPEM(caPEM []byte) {
	i.caPEM = caPEM
}

func (i *webhookDescriptionWithCAPEM) getCAPEM() []byte {
	return i.caPEM
}

func getServicePorts(descs []certResource) []corev1.ServicePort {
	result := []corev1.ServicePort{}
	for _, desc := range descs {
		if !containsServicePort(result, desc.getServicePort()) {
			result = append(result, desc.getServicePort())
		}
	}

	return result
}

func (i *webhookDescriptionWithCAPEM) getDeploymentName() string {
	return i.webhookDescription.DeploymentName
}

func (i *webhookDescriptionWithCAPEM) getServicePort() corev1.ServicePort {
	containerPort := 443
	if i.webhookDescription.ContainerPort > 0 {
		containerPort = int(i.webhookDescription.ContainerPort)
	}

	// Before users could set TargetPort in the CSV, OLM just set its
	// value to the containerPort. This change keeps OLM backwards compatible
	// if the TargetPort is not set in the CSV.
	targetPort := intstr.FromInt(containerPort)
	if i.webhookDescription.TargetPort != nil {
		targetPort = *i.webhookDescription.TargetPort
	}
	return corev1.ServicePort{
		Name:       strconv.Itoa(containerPort),
		Port:       int32(containerPort),
		TargetPort: targetPort,
	}
}

func SecretName(serviceName string) string {
	return serviceName + "-cert"
}

func ServiceName(deploymentName string) string {
	return deploymentName + "-service"
}
