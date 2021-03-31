package controllers

import (
	"context"
	"fmt"

	"github.com/buptGophers/operator-manager/api/v1alpha1"
	"github.com/buptGophers/operator-manager/controllers/clusterserviceversion_controller/util/ownerutil"
	log "github.com/sirupsen/logrus"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

func (r *ClusterServiceVersionReconciler) createOrUpdateAPIService(csv *v1alpha1.ClusterServiceVersion, caPEM []byte, desc v1alpha1.APIServiceDescription) error {
	apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)
	logger := log.WithFields(log.Fields{
		"owner":      csv.GetName(),
		"namespace":  csv.GetNamespace(),
		"apiservice": fmt.Sprintf("%s.%s", desc.Version, desc.Group),
	})

	apiService := &apiregistrationv1.APIService{
		Spec: apiregistrationv1.APIServiceSpec{
			Group:                desc.Group,
			Version:              desc.Version,
			GroupPriorityMinimum: int32(2000),
			VersionPriority:      int32(15),
		},
	}
	apiService.SetName(apiServiceName)

	// Add the CSV as an owner
	ownerutil.AddOwnerLabels(apiService, csv)

	// Create a service for the deployment
	containerPort := int32(443)
	if desc.ContainerPort > 0 {
		containerPort = desc.ContainerPort
	}
	// update the ServiceReference
	apiService.Spec.Service = &apiregistrationv1.ServiceReference{
		Namespace: csv.GetNamespace(),
		Name:      ServiceName(desc.DeploymentName),
		Port:      &containerPort,
	}

	// create a fresh CA bundle
	apiService.Spec.CABundle = caPEM

	// attempt a update or create
	err := r.Client.Create(context.TODO(), apiService)
	if err != nil {
		logger.Warnf("could not create or update APIService")
		return err
	}

	return nil
}

func IsAPIServiceAvailable(apiService *apiregistrationv1.APIService) bool {
	for _, c := range apiService.Status.Conditions {
		if c.Type == apiregistrationv1.Available && c.Status == apiregistrationv1.ConditionTrue {
			return true
		}
	}
	return false
}
