package controllers

import (
	"context"
	"fmt"
	"github.com/buptGophers/operator-manager/api/v1alpha1"
	"github.com/buptGophers/operator-manager/controllers/clusterserviceversion_controller/util/ownerutil"
	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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

		if len(certResources) == 0 {
			log.Info("No api or webhook descs to add CA to")
			continue
		}

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
	//existingService, err := r.Client.Get(, service.GetName())
	//if err == nil {
	//	if !ownerutil.Adoptable(csv, existingService.GetOwnerReferences()) {
	//		return nil, nil, fmt.Errorf("service %s not safe to replace: extraneous ownerreferences found", service.GetName())
	//	}
	//	service.SetOwnerReferences(existingService.GetOwnerReferences())
	//
	//	// Delete the Service to replace
	//	deleteErr := i.strategyClient.GetOpClient().DeleteService(service.GetNamespace(), service.GetName(), &metav1.DeleteOptions{})
	//	if err != nil && !k8serrors.IsNotFound(deleteErr) {
	//		return nil, nil, fmt.Errorf("could not delete existing service %s", service.GetName())
	//	}
	//}
	// Attempt to create the Service
	err := r.Client.Create(context.TODO(), service)
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

	//existingSecret, err := i.strategyClient.GetOpLister().CoreV1().SecretLister().Secrets(i.owner.GetNamespace()).Get(secret.GetName())
	//if err == nil {
	//	// Check if the only owners are this CSV or in this CSV's replacement chain
	//	if ownerutil.Adoptable(i.owner, existingSecret.GetOwnerReferences()) {
	//		ownerutil.AddNonBlockingOwner(secret, i.owner)
	//	}
	//
	//	// Attempt an update
	//	// TODO: Check that the secret was not modified
	//	if existingCAPEM, ok := existingSecret.Data[OLMCAPEMKey]; ok && !ShouldRotateCerts(i.owner.(*v1alpha1.ClusterServiceVersion)) {
	//		logger.Warnf("reusing existing cert %s", secret.GetName())
	//		secret = existingSecret
	//		caPEM = existingCAPEM
	//		caHash = certs.PEMSHA256(caPEM)
	//	} else if _, err := i.strategyClient.GetOpClient().UpdateSecret(secret); err != nil {
	//		logger.Warnf("could not update secret %s", secret.GetName())
	//		return nil, nil, err
	//	}
	//
	//} else if k8serrors.IsNotFound(err) {
	//	// Create the secret
	//	ownerutil.AddNonBlockingOwner(secret, i.owner)
	//	_, err = i.strategyClient.GetOpClient().CreateSecret(secret)
	//	if err != nil {
	//		log.Warnf("could not create secret %s", secret.GetName())
	//		return nil, nil, err
	//	}
	//} else {
	//	return nil, nil, err
	//}
	ownerutil.AddNonBlockingOwner(secret, csv)
	// Attempt to create the Service
	err = r.Client.Create(context.TODO(), secret)
	if err != nil {
		log.Warnf("could not create secret %s", secret.GetName())
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

	//existingSecretRole, err := i.strategyClient.GetOpLister().RbacV1().RoleLister().Roles(i.owner.GetNamespace()).Get(secretRole.GetName())
	//if err == nil {
	//	// Check if the only owners are this CSV or in this CSV's replacement chain
	//	if ownerutil.Adoptable(i.owner, existingSecretRole.GetOwnerReferences()) {
	//		ownerutil.AddNonBlockingOwner(secretRole, i.owner)
	//	}
	//
	//	// Attempt an update
	//	if _, err := i.strategyClient.GetOpClient().UpdateRole(secretRole); err != nil {
	//		logger.Warnf("could not update secret role %s", secretRole.GetName())
	//		return nil, nil, err
	//	}
	//} else if k8serrors.IsNotFound(err) {
	//	// Create the role
	//	ownerutil.AddNonBlockingOwner(secretRole, i.owner)
	//	_, err = i.strategyClient.GetOpClient().CreateRole(secretRole)
	//	if err != nil {
	//		log.Warnf("could not create secret role %s", secretRole.GetName())
	//		return nil, nil, err
	//	}
	//} else {
	//	return nil, nil, err
	//}
	ownerutil.AddNonBlockingOwner(secretRole, csv)
	// Attempt to create the Service
	err = r.Client.Create(context.TODO(), secretRole)
	if err != nil {
		log.Warnf("could not create secretRole %s", secretRole.GetName())
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

	//existingSecretRoleBinding, err := i.strategyClient.GetOpLister().RbacV1().RoleBindingLister().RoleBindings(i.owner.GetNamespace()).Get(secretRoleBinding.GetName())
	//if err == nil {
	//	// Check if the only owners are this CSV or in this CSV's replacement chain
	//	if ownerutil.Adoptable(i.owner, existingSecretRoleBinding.GetOwnerReferences()) {
	//		ownerutil.AddNonBlockingOwner(secretRoleBinding, i.owner)
	//	}
	//
	//	// Attempt an update
	//	if _, err := i.strategyClient.GetOpClient().UpdateRoleBinding(secretRoleBinding); err != nil {
	//		logger.Warnf("could not update secret rolebinding %s", secretRoleBinding.GetName())
	//		return nil, nil, err
	//	}
	//} else if k8serrors.IsNotFound(err) {
	//	// Create the role
	//	ownerutil.AddNonBlockingOwner(secretRoleBinding, i.owner)
	//	_, err = i.strategyClient.GetOpClient().CreateRoleBinding(secretRoleBinding)
	//	if err != nil {
	//		log.Warnf("could not create secret rolebinding with dep spec: %#v", depSpec)
	//		return nil, nil, err
	//	}
	//} else {
	//	return nil, nil, err
	//}
	ownerutil.AddNonBlockingOwner(secretRoleBinding, csv)
	// Attempt to create the Service
	err = r.Client.Create(context.TODO(), secretRoleBinding)
	if err != nil {
		log.Warnf("could not create secretRoleBinding %s", secretRoleBinding.GetName())
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

	//existingAuthDelegatorClusterRoleBinding, err := i.strategyClient.GetOpLister().RbacV1().ClusterRoleBindingLister().Get(authDelegatorClusterRoleBinding.GetName())
	//if err == nil {
	//	// Check if the only owners are this CSV or in this CSV's replacement chain.
	//	if ownerutil.AdoptableLabels(existingAuthDelegatorClusterRoleBinding.GetLabels(), true, i.owner) {
	//		logger.WithFields(log.Fields{"obj": "authDelegatorCRB", "labels": existingAuthDelegatorClusterRoleBinding.GetLabels()}).Debug("adopting")
	//		if err := ownerutil.AddOwnerLabels(authDelegatorClusterRoleBinding, i.owner); err != nil {
	//			return nil, nil, err
	//		}
	//	}
	//
	//	// Attempt an update.
	//	if _, err := i.strategyClient.GetOpClient().UpdateClusterRoleBinding(authDelegatorClusterRoleBinding); err != nil {
	//		logger.Warnf("could not update auth delegator clusterrolebinding %s", authDelegatorClusterRoleBinding.GetName())
	//		return nil, nil, err
	//	}
	//} else if k8serrors.IsNotFound(err) {
	//	// Create the role.
	//	if err := ownerutil.AddOwnerLabels(authDelegatorClusterRoleBinding, i.owner); err != nil {
	//		return nil, nil, err
	//	}
	//	_, err = i.strategyClient.GetOpClient().CreateClusterRoleBinding(authDelegatorClusterRoleBinding)
	//	if err != nil {
	//		log.Warnf("could not create auth delegator clusterrolebinding %s", authDelegatorClusterRoleBinding.GetName())
	//		return nil, nil, err
	//	}
	//} else {
	//	return nil, nil, err
	//}
	ownerutil.AddOwnerLabels(authDelegatorClusterRoleBinding, csv)
	err = r.Client.Create(context.TODO(), authDelegatorClusterRoleBinding)
	if err != nil {
		log.Warnf("could not create auth delegator clusterrolebinding %s", authDelegatorClusterRoleBinding.GetName())
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

	//existingAuthReaderRoleBinding, err := i.strategyClient.GetOpLister().RbacV1().RoleBindingLister().RoleBindings(KubeSystem).Get(authReaderRoleBinding.GetName())
	//if err == nil {
	//	// Check if the only owners are this CSV or in this CSV's replacement chain.
	//	if ownerutil.AdoptableLabels(existingAuthReaderRoleBinding.GetLabels(), true, i.owner) {
	//		logger.WithFields(log.Fields{"obj": "existingAuthReaderRB", "labels": existingAuthReaderRoleBinding.GetLabels()}).Debug("adopting")
	//		if err := ownerutil.AddOwnerLabels(authReaderRoleBinding, i.owner); err != nil {
	//			return nil, nil, err
	//		}
	//	}
	//	// Attempt an update.
	//	if _, err := i.strategyClient.GetOpClient().UpdateRoleBinding(authReaderRoleBinding); err != nil {
	//		logger.Warnf("could not update auth reader role binding %s", authReaderRoleBinding.GetName())
	//		return nil, nil, err
	//	}
	//} else if k8serrors.IsNotFound(err) {
	//	// Create the role.
	//	if err := ownerutil.AddOwnerLabels(authReaderRoleBinding, i.owner); err != nil {
	//		return nil, nil, err
	//	}
	//	_, err = i.strategyClient.GetOpClient().CreateRoleBinding(authReaderRoleBinding)
	//	if err != nil {
	//		log.Warnf("could not create auth reader role binding %s", authReaderRoleBinding.GetName())
	//		return nil, nil, err
	//	}
	//} else {
	//	return nil, nil, err
	//}
	ownerutil.AddOwnerLabels(authReaderRoleBinding, csv)
	err = r.Client.Create(context.TODO(), authDelegatorClusterRoleBinding)
	if err != nil {
		log.Warnf("could not create auth reader role binding %s", authReaderRoleBinding.GetName())
		return nil, nil, err
	}
	// AddDefaultCertVolumeAndVolumeMounts(&depSpec, secret.GetName())

	// Setting the olm hash label forces a rollout and ensures that the new secret
	// is used by the apiserver if not hot reloading.
	depSpec.Template.ObjectMeta.SetAnnotations(map[string]string{OLMCAHashAnnotationKey: caHash})

	return &depSpec, caPEM, nil
}

func (r *ClusterServiceVersionReconciler) certResourcesForDeployment(csv *v1alpha1.ClusterServiceVersion, deploymentName string) []certResource {
	result := []certResource{}
	for _, desc := range r.getCertResources(csv) {
		fmt.Println("-------------------------", desc)
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
