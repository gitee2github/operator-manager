package controllers

import (
	"context"
	"fmt"
	"github.com/buptGophers/operator-manager/api/v1alpha1"
	"github.com/buptGophers/operator-manager/controllers/clusterserviceversion_controller/util/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"time"
)

func (r *ClusterServiceVersionReconciler) installCertRequirements(csv *v1alpha1.ClusterServiceVersion, strategy Strategy) (*v1alpha1.StrategyDetailsDeployment, error) {
	logger := log.WithFields(log.Fields{})

	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		return nil, fmt.Errorf("unsupported InstallStrategy type")
	}

	// Create the CA
	expiration := time.Now().Add(DefaultCertValidFor)
	ca, err := GenerateCA(expiration, Organization)
	if err != nil {
		logger.Debug("failed to generate CA")
		return nil, err
	}
	rotateAt := expiration.Add(-1 * DefaultCertMinFresh)
	for n, sddSpec := range strategyDetailsDeployment.DeploymentSpecs {
		certResources := r.certResourcesForDeployment(csv, sddSpec.Name)

		//if len(certResources) == 0 {
		//	log.Info("No api or webhook descs to add CA to")
		//	continue
		//}

		// Update the deployment for each certResource
		newDepSpec, caPEM, err := r.installCertRequirementsForDeployment(csv, sddSpec.Name, ca, rotateAt, sddSpec.Spec, getServicePorts(certResources))
		if err != nil {
			return nil, err
		}

		r.updateCertResourcesForDeployment(csv, sddSpec.Name, caPEM)

		strategyDetailsDeployment.DeploymentSpecs[n].Spec = *newDepSpec
	}
	return strategyDetailsDeployment, nil
}

func (r *ClusterServiceVersionReconciler) installCertRequirementsForDeployment(csv *v1alpha1.ClusterServiceVersion, deploymentName string, ca *KeyPair, rotateAt time.Time, depSpec appsv1.DeploymentSpec, ports []corev1.ServicePort) (*appsv1.DeploymentSpec, []byte, error) {
	logger := log.WithFields(log.Fields{})
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	config, err := k8sconfig.ClientConfig()
	if err != nil {
		return nil, nil, err
	}

	// Retrieve server k8s version
	kubernetesClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}
	servPort := corev1.ServicePort{
		Port: int32(443),
	}
	ports = append(ports, servPort)
	// Create a service for the deployment
	service := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports:    ports,
			Selector: depSpec.Selector.MatchLabels,
		},
	}
	service.SetName(ServiceName(deploymentName))
	service.SetNamespace(csv.GetNamespace())
	ownerutil.AddNonBlockingOwner(service, csv)
	existingService, err := kubernetesClient.CoreV1().Services(csv.GetNamespace()).Get(context.TODO(), service.Name, metav1.GetOptions{})
	if err == nil {
		if !ownerutil.Adoptable(csv, existingService.GetOwnerReferences()) {
			return nil, nil, fmt.Errorf("service %s not safe to replace: extraneous ownerreferences found", service.GetName())
		}
		service.SetOwnerReferences(existingService.GetOwnerReferences())

		// Delete the Service to replace
		deleteErr := kubernetesClient.CoreV1().Services(csv.GetNamespace()).Delete(context.TODO(), service.Name, metav1.DeleteOptions{})
		if deleteErr != nil && !k8serrors.IsNotFound(deleteErr) {
			return nil, nil, fmt.Errorf("could not delete existing service %s", service.GetName())
		}
	}
	// Attempt to create the Service
	service, err = kubernetesClient.CoreV1().Services(csv.GetNamespace()).Create(context.TODO(), service, metav1.CreateOptions{})
	if err != nil {
		logger.Warnf("could not create service %s", service.GetName())
		return nil, nil, fmt.Errorf("could not create service %s: %s", service.GetName(), err.Error())
	}

	// Create signed serving cert
	hosts := []string{
		fmt.Sprintf("%s.%s", service.GetName(), csv.GetNamespace()),
		fmt.Sprintf("%s.%s.svc", service.GetName(), csv.GetNamespace()),
	}
	servingPair, err := certGenerator.Generate(rotateAt, Organization, ca, hosts)
	if err != nil {
		logger.Warnf("could not generate signed certs for hosts %v", hosts)
		return nil, nil, err
	}

	// Create Secret for serving cert
	certPEM, privPEM, err := servingPair.ToPEM()
	if err != nil {
		logger.Warnf("unable to convert serving certificate and private key to PEM format for Service %s", service.GetName())
		return nil, nil, err
	}

	// Add olmcahash as a label to the caPEM
	caPEM, _, err := ca.ToPEM()
	if err != nil {
		logger.Warnf("unable to convert CA certificate to PEM format for Service %s", service)
		return nil, nil, err
	}
	caHash := PEMSHA256(caPEM)

	secret := &corev1.Secret{
		Data: map[string][]byte{
			"tls.crt":   certPEM,
			"tls.key":   privPEM,
			OLMCAPEMKey: caPEM,
		},
		Type: corev1.SecretTypeTLS,
	}
	secret.SetName(SecretName(service.GetName()))
	secret.SetNamespace(csv.GetNamespace())
	secret.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})

	existingSecret, err := kubernetesClient.CoreV1().Secrets(csv.GetNamespace()).Get(context.TODO(), secret.Name, metav1.GetOptions{})
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(csv, existingSecret.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(existingSecret, csv)
		}

		// Attempt an update
		// TODO: Check that the secret was not modified
		if existingCAPEM, ok := existingSecret.Data[OLMCAPEMKey]; ok && !ShouldRotateCerts(csv) {
			logger.Warnf("reusing existing cert %s", existingSecret.GetName())
			caPEM = existingCAPEM
			caHash = certs.PEMSHA256(caPEM)
		} else if _, err := kubernetesClient.CoreV1().Secrets(csv.GetNamespace()).Update(context.TODO(), secret, metav1.UpdateOptions{}); err != nil {
			logger.Warnf("could not update secret %s", secret.GetName())
			return nil, nil, err
		}

	} else if k8serrors.IsNotFound(err) {
		// Create the secret
		ownerutil.AddNonBlockingOwner(secret, csv)
		_, err = kubernetesClient.CoreV1().Secrets(csv.GetNamespace()).Create(context.TODO(), secret, metav1.CreateOptions{})
		if err != nil {
			log.Warnf("could not create secret %s", secret.GetName())
			return nil, nil, err
		}
	} else {
		return nil, nil, err
	}

	// create Role and RoleBinding to allow the deployment to mount the Secret
	secretRole := &rbacv1.Role{
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:         []string{"get"},
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{secret.GetName()},
			},
		},
	}
	secretRole.SetName(secret.GetName())
	secretRole.SetNamespace(csv.GetNamespace())

	existingSecretRole, err := kubernetesClient.RbacV1().Roles(csv.GetNamespace()).Get(context.TODO(), secretRole.Name, metav1.GetOptions{})
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(csv, existingSecretRole.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(secretRole, csv)
		}

		// Attempt an update
		if _, err := kubernetesClient.RbacV1().Roles(csv.GetNamespace()).Update(context.TODO(), secretRole, metav1.UpdateOptions{}); err != nil {
			logger.Warnf("could not update secret role %s", secretRole.GetName())
			return nil, nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role
		ownerutil.AddNonBlockingOwner(secretRole, csv)
		_, err = kubernetesClient.RbacV1().Roles(csv.GetNamespace()).Create(context.TODO(), secretRole, metav1.CreateOptions{})
		if err != nil {
			log.Warnf("could not create secret role %s", secretRole.GetName())
			return nil, nil, err
		}
	} else {
		return nil, nil, err
	}

	if depSpec.Template.Spec.ServiceAccountName == "" {
		depSpec.Template.Spec.ServiceAccountName = "default"
	}

	secretRoleBinding := &rbacv1.RoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      depSpec.Template.Spec.ServiceAccountName,
				Namespace: csv.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     secretRole.GetName(),
		},
	}
	secretRoleBinding.SetName(secret.GetName())
	secretRoleBinding.SetNamespace(csv.GetNamespace())

	existingSecretRoleBinding, err := kubernetesClient.RbacV1().RoleBindings(csv.GetNamespace()).Get(context.TODO(), secretRoleBinding.Name, metav1.GetOptions{})
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain
		if ownerutil.Adoptable(csv, existingSecretRoleBinding.GetOwnerReferences()) {
			ownerutil.AddNonBlockingOwner(secretRoleBinding, csv)
		}

		// Attempt an update
		if _, err := kubernetesClient.RbacV1().RoleBindings(csv.GetNamespace()).Update(context.TODO(), secretRoleBinding, metav1.UpdateOptions{}); err != nil {
			logger.Warnf("could not update secret rolebinding %s", secretRoleBinding.GetName())
			return nil, nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role
		ownerutil.AddNonBlockingOwner(secretRoleBinding, csv)
		_, err = kubernetesClient.RbacV1().RoleBindings(csv.GetNamespace()).Create(context.TODO(), secretRoleBinding, metav1.CreateOptions{})
		if err != nil {
			log.Warnf("could not create secret rolebinding with dep spec: %#v", depSpec)
			return nil, nil, err
		}
	} else {
		return nil, nil, err
	}

	// create ClusterRoleBinding to system:auth-delegator Role
	authDelegatorClusterRoleBinding := &rbacv1.ClusterRoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      depSpec.Template.Spec.ServiceAccountName,
				Namespace: csv.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:auth-delegator",
		},
	}
	authDelegatorClusterRoleBinding.SetName(service.GetName() + "-system:auth-delegator")

	existingAuthDelegatorClusterRoleBinding, err := kubernetesClient.RbacV1().ClusterRoleBindings().Get(context.TODO(), authDelegatorClusterRoleBinding.Name, metav1.GetOptions{})
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain.
		if ownerutil.AdoptableLabels(existingAuthDelegatorClusterRoleBinding.GetLabels(), true, csv) {
			logger.WithFields(log.Fields{"obj": "authDelegatorCRB", "labels": existingAuthDelegatorClusterRoleBinding.GetLabels()}).Debug("adopting")
			if err := ownerutil.AddOwnerLabels(authDelegatorClusterRoleBinding, csv); err != nil {
				return nil, nil, err
			}
		}

		// Attempt an update.
		if _, err := kubernetesClient.RbacV1().ClusterRoleBindings().Update(context.TODO(), authDelegatorClusterRoleBinding, metav1.UpdateOptions{}); err != nil {
			logger.Warnf("could not update auth delegator clusterrolebinding %s", authDelegatorClusterRoleBinding.GetName())
			return nil, nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role.
		if err := ownerutil.AddOwnerLabels(authDelegatorClusterRoleBinding, csv); err != nil {
			return nil, nil, err
		}
		_, err = kubernetesClient.RbacV1().ClusterRoleBindings().Create(context.TODO(), authDelegatorClusterRoleBinding, metav1.CreateOptions{})
		if err != nil {
			log.Warnf("could not create auth delegator clusterrolebinding %s", authDelegatorClusterRoleBinding.GetName())
			return nil, nil, err
		}
	} else {
		return nil, nil, err
	}

	// Create RoleBinding to extension-apiserver-authentication-reader Role in the kube-system namespace.
	authReaderRoleBinding := &rbacv1.RoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      depSpec.Template.Spec.ServiceAccountName,
				Namespace: csv.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "extension-apiserver-authentication-reader",
		},
	}
	authReaderRoleBinding.SetName(service.GetName() + "-auth-reader")
	authReaderRoleBinding.SetNamespace(KubeSystem)

	existingAuthReaderRoleBinding, err := kubernetesClient.RbacV1().RoleBindings(KubeSystem).Get(context.TODO(), authReaderRoleBinding.Name, metav1.GetOptions{})
	if err == nil {
		// Check if the only owners are this CSV or in this CSV's replacement chain.
		if ownerutil.AdoptableLabels(existingAuthReaderRoleBinding.GetLabels(), true, csv) {
			logger.WithFields(log.Fields{"obj": "existingAuthReaderRB", "labels": existingAuthReaderRoleBinding.GetLabels()}).Debug("adopting")
			if err := ownerutil.AddOwnerLabels(authReaderRoleBinding, csv); err != nil {
				return nil, nil, err
			}
		}
		// Attempt an update.
		if _, err := kubernetesClient.RbacV1().RoleBindings(KubeSystem).Update(context.TODO(), authReaderRoleBinding, metav1.UpdateOptions{}); err != nil {
			logger.Warnf("could not update auth reader role binding %s", authReaderRoleBinding.GetName())
			return nil, nil, err
		}
	} else if k8serrors.IsNotFound(err) {
		// Create the role.
		if err := ownerutil.AddOwnerLabels(authReaderRoleBinding, csv); err != nil {
			return nil, nil, err
		}
		_, err = kubernetesClient.RbacV1().RoleBindings(KubeSystem).Create(context.TODO(), authReaderRoleBinding, metav1.CreateOptions{})
		if err != nil {
			log.Warnf("could not create auth reader role binding %s", authReaderRoleBinding.GetName())
			return nil, nil, err
		}
	} else {
		return nil, nil, err
	}
	// AddDefaultCertVolumeAndVolumeMounts(&depSpec, secret.GetName())

	// Setting the olm hash label forces a rollout and ensures that the new secret
	// is used by the apiserver if not hot reloading.
	depSpec.Template.ObjectMeta.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})

	return &depSpec, caPEM, nil
}

func (r *ClusterServiceVersionReconciler) certResourcesForDeployment(csv *v1alpha1.ClusterServiceVersion, deploymentName string) []certResource {
	var result []certResource
	for _, desc := range r.getCertResources(csv) {
		if desc.getDeploymentName() == deploymentName {
			result = append(result, desc)
		}
	}
	return result
}

func (r *ClusterServiceVersionReconciler) updateCertResourcesForDeployment(csv *v1alpha1.ClusterServiceVersion, deploymentName string, caPEM []byte) {
	for _, desc := range r.certResourcesForDeployment(csv, deploymentName) {
		desc.setCAPEM(caPEM)
	}
}

func (r *ClusterServiceVersionReconciler) getCertResources(csv *v1alpha1.ClusterServiceVersion) []certResource {
	apiDescs := make([]certResource, len(csv.GetAllAPIServiceDescriptions()))
	for i := range csv.GetAllAPIServiceDescriptions() {
		apiDescs[i] = &apiServiceDescriptionsWithCAPEM{csv.GetAllAPIServiceDescriptions()[i], []byte{}}
	}

	webhookDescs := make([]certResource, len(csv.Spec.WebhookDefinitions))
	for i := range csv.Spec.WebhookDefinitions {
		webhookDescs[i] = &webhookDescriptionWithCAPEM{csv.Spec.WebhookDefinitions[i], []byte{}}
	}
	return append(apiDescs, webhookDescs...)
}

func ShouldRotateCerts(csv *v1alpha1.ClusterServiceVersion) bool {
	now := metav1.Now()
	if !csv.Status.CertsRotateAt.IsZero() && csv.Status.CertsRotateAt.Before(&now) {
		return true
	}

	return false
}
