/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	util "github.com/buptGophers/operator-manager/controllers/clusterserviceversion_controller/util"
	"github.com/buptGophers/operator-manager/controllers/clusterserviceversion_controller/util/ownerutil"
	"github.com/coreos/go-semver/semver"
	"github.com/go-logr/logr"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	log "github.com/sirupsen/logrus"
	"hash/fnv"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilclock "k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/client-go/tools/clientcmd"
	apiregistration "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/buptGophers/operator-manager/api/v1alpha1"
)

const (
	StrategyErrReasonComponentMissing   = "ComponentMissing"
	StrategyErrReasonAnnotationsMissing = "AnnotationsMissing"
	StrategyErrReasonWaiting            = "Waiting"
	StrategyErrReasonInvalidStrategy    = "InvalidStrategy"
	StrategyErrReasonTimeout            = "Timeout"
	StrategyErrReasonUnknown            = "Unknown"
	StrategyErrBadPatch                 = "PatchUnsuccessful"
	StrategyErrDeploymentUpdated        = "DeploymentUpdated"
	StrategyErrInsufficientPermissions  = "InsufficentPermissions"
)

// unrecoverableErrors are the set of errors that mean we can't recover an install strategy
var unrecoverableErrors = map[string]struct{}{
	StrategyErrReasonInvalidStrategy:   {},
	StrategyErrReasonTimeout:           {},
	StrategyErrBadPatch:                {},
	StrategyErrInsufficientPermissions: {},
}

// StrategyError is used to represent error types for install strategies
type StrategyError struct {
	Reason  string
	Message string
}

// ClusterServiceVersionReconciler reconciles a ClusterServiceVersion object
type ClusterServiceVersionReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

const DeploymentSpecHashLabelKey = "olm.deployment-spec-hash"

type Strategy interface {
	GetStrategyName() string
}

// +kubebuilder:rbac:groups=operator.operator.domain,resources=clusterserviceversions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operator.operator.domain,resources=clusterserviceversions/status,verbs=get;update;patch
func (r *ClusterServiceVersionReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	_ = r.Log.WithValues("clusterserviceversion", req.NamespacedName)

	// List ClusterServiceVersion
	reqLogger := r.Log.WithValues("Request Namespace", req.Namespace)
	reqLogger.Info("Reconciling ClusterServiceVersion")

	csvList := &v1alpha1.ClusterServiceVersionList{}
	err := r.Client.List(context.TODO(), csvList)
	if err != nil {
		if err := client.IgnoreNotFound(err); err == nil {
			reqLogger.Info("Cannot found any ClusterServiceVersion in ", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		reqLogger.Error(err, "unexpected error!")
		return ctrl.Result{}, err
	}

	for _, csv := range csvList.Items {
		switch csv.Status.Phase {
		case v1alpha1.CSVPhasePending:
			strategy, err := r.UnmarshalStrategy(&csv)
			if err != nil {
				return ctrl.Result{}, err
			}
			// Check
			met, statuses, err := r.requirementAndPermissionStatus(&csv, strategy)
			if err != nil {
				// TODO: account for Bad Rule as well
				reqLogger.Info("invalid install strategy")
				return ctrl.Result{}, err
			}
			csv.SetRequirementStatus(statuses)

			if !met {
				reqLogger.Info("requirements were not met")
				return ctrl.Result{}, err
			}
			// Create a map to track unique names
			webhookNames := map[string]struct{}{}
			// Check if Webhooks have valid rules and unique names
			// TODO: Move this to validating library
			for _, desc := range csv.Spec.WebhookDefinitions {
				_, present := webhookNames[desc.GenerateName]
				if present {
					reqLogger.Error(err, "Repeated WebhookDescription name %s", desc.GenerateName)
					return ctrl.Result{}, err
				}
				webhookNames[desc.GenerateName] = struct{}{}
				if err = ValidWebhookRules(desc.Rules); err != nil {
					reqLogger.Error(err, "WebhookDescription %s includes invalid rules %s", desc.GenerateName)
					return ctrl.Result{}, err
				}
			}

			// Install owned APIServices and update strategy with serving cert data
			updatedStrategy, err := r.installCertRequirements(&csv, strategy)
			if err != nil {
				reqLogger.Error(err, "failed to update strategy")
				return ctrl.Result{}, nil
			}

			for _, strategyDetailsDeployment := range updatedStrategy.DeploymentSpecs {
				// Install a operator strategy as a dep into cluster
				dep, err := r.installDeployment(&csv, &strategyDetailsDeployment)
				if err != nil {
					reqLogger.Error(err, "failed to install strategy")
					return ctrl.Result{}, nil
				}

				err = r.createOrUpdateCertResourcesForDeployment(&csv)
				if err != nil {
					reqLogger.Error(err, "failed to generate CA")
					return ctrl.Result{}, nil
				}

				reason, ready, err := DeploymentStatus(dep)
				if err != nil {
					log.Debugf("deployment %s not ready before timeout: %s", dep.Name, err.Error())
					log.Error(StrategyError{Reason: StrategyErrReasonTimeout, Message: fmt.Sprintf("deployment %s not ready before timeout: %s", dep.Name, err.Error())})
				}
				if !ready {
					log.Info(StrategyError{Reason: StrategyErrReasonWaiting, Message: fmt.Sprintf(" for deployment %s to become ready: %s", dep.Name, reason)})
				}

				// check annotations
				if len(csv.GetAnnotations()) > 0 && dep.Spec.Template.Annotations == nil {
					log.Error(StrategyError{Reason: StrategyErrReasonAnnotationsMissing, Message: fmt.Sprintf("no annotations found on deployment")})
				}
				for key, value := range csv.GetAnnotations() {
					if actualValue, ok := dep.Spec.Template.Annotations[key]; !ok {
						log.Error(StrategyError{Reason: StrategyErrReasonAnnotationsMissing, Message: fmt.Sprintf("annotations on deployment does not contain expected key: %s", key)})
					} else if dep.Spec.Template.Annotations[key] != value {
						log.Error(StrategyError{Reason: StrategyErrReasonAnnotationsMissing, Message: fmt.Sprintf("unexpected annotation on deployment. Expected %s:%s, found %s:%s", key, value, key, actualValue)})
					}
				}
			}
			csv.SetPhase(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallSuccessful, "Cluster resources changed state", now())
			if err := r.Client.Update(context.TODO(), &csv); err != nil {
				reqLogger.Info("Update error!")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil

		case v1alpha1.CSVPhaseDeleting:
			if err := r.handleCSVDeletion(reqLogger, &csv); err != nil {
				reqLogger.Error(err, err.Error())
				return ctrl.Result{}, err
			}

		default:
			break
		}

	}

	return ctrl.Result{}, nil
}

// HashDeploymentSpec calculates a hash given a copy of the deployment spec from a CSV, stripping any
// operatorgroup annotations.
func HashDeploymentSpec(spec appsv1.DeploymentSpec) string {
	hasher := fnv.New32a()
	util.DeepHashObject(hasher, &spec)
	return rand.SafeEncodeString(fmt.Sprint(hasher.Sum32()))
}

// DeploymentInitializerFunc takes a deployment object and appropriately
// initializes it for install.
// Before a deployment is created on the cluster, we can run a series of
// overrides functions that will properly initialize the deployment object.
type DeploymentInitializerFunc func(deployment *appsv1.Deployment) error

// DeploymentInitializerFuncChain defines a chain of DeploymentInitializerFunc.
type DeploymentInitializerFuncChain []DeploymentInitializerFunc

func (r *ClusterServiceVersionReconciler) requirementAndPermissionStatus(csv *v1alpha1.ClusterServiceVersion, strategy Strategy) (bool, []v1alpha1.RequirementStatus, error) {
	var allReqStatuses []v1alpha1.RequirementStatus
	strategyDetailsDeployment, ok := strategy.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		log.Error("failed to generate CA")
		return false, nil, fmt.Errorf("could not cast install strategy as type %T", strategyDetailsDeployment)
	}

	// Check kubernetes version requirement between CSV and server
	minKubeMet, minKubeStatus := r.minKubeVersionStatus(csv.GetName(), csv.Spec.MinKubeVersion)
	if minKubeStatus != nil {
		allReqStatuses = append(allReqStatuses, minKubeStatus...)
	}

	reqMet, reqStatuses := r.requirementStatus(strategyDetailsDeployment, csv.GetAllCRDDescriptions(), csv.GetOwnedAPIServiceDescriptions(), csv.GetRequiredAPIServiceDescriptions(), csv.Spec.NativeAPIs)
	allReqStatuses = append(allReqStatuses, reqStatuses...)

	//rules := clientcmd.NewDefaultClientConfigLoadingRules()
	//k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	//config, err := k8sconfig.ClientConfig()

	//// Retrieve server k8s version
	//kubernetesClient, err := kubernetes.NewForConfig(config)
	//
	rbacLister := operatorlister.NewLister().RbacV1()
	//
	roleLister := rbacLister.RoleLister()
	roleBindingLister := rbacLister.RoleBindingLister()
	clusterRoleLister := rbacLister.ClusterRoleLister()
	clusterRoleBindingLister := rbacLister.ClusterRoleBindingLister()
	ruleChecker := NewCSVRuleChecker(roleLister, roleBindingLister, clusterRoleLister, clusterRoleBindingLister, csv)
	permMet, permStatuses, err := r.permissionStatus(strategyDetailsDeployment, ruleChecker, csv.GetNamespace(), csv)
	if err != nil {
		return false, nil, err
	}

	// Aggregate requirement and permissions statuses
	statuses := append(allReqStatuses, permStatuses...)
	met := minKubeMet && reqMet && permMet
	if !met {
		r.Log.WithValues("minKubeMet", minKubeMet).WithValues("reqMet", reqMet).WithValues("permMet", permMet).Info("permissions/requirements not met")
	}

	return met, statuses, nil
}

func (r *ClusterServiceVersionReconciler) minKubeVersionStatus(name string, minKubeVersion string) (met bool, statuses []v1alpha1.RequirementStatus) {
	if minKubeVersion == "" {
		return true, nil
	}

	status := v1alpha1.RequirementStatus{
		Group:   "operators.coreos.com",
		Version: "v1alpha1",
		Kind:    "ClusterServiceVersion",
		Name:    name,
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	config, err := k8sconfig.ClientConfig()

	// Retrieve server k8s version
	kubernetesClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return
	}
	serverVersionInfo, err := kubernetesClient.Discovery().ServerVersion()
	if err != nil {
		status.Status = v1alpha1.RequirementStatusReasonPresentNotSatisfied
		status.Message = "Server version discovery error"
		met = false
		statuses = append(statuses, status)
		return
	}

	serverVersion, err := semver.NewVersion(strings.Split(strings.TrimPrefix(serverVersionInfo.String(), "v"), "-")[0])
	if err != nil {
		status.Status = v1alpha1.RequirementStatusReasonPresentNotSatisfied
		status.Message = "Server version parsing error"
		met = false
		statuses = append(statuses, status)
		return
	}

	csvVersionInfo, err := semver.NewVersion(strings.TrimPrefix(minKubeVersion, "v"))
	if err != nil {
		status.Status = v1alpha1.RequirementStatusReasonPresentNotSatisfied
		status.Message = "CSV version parsing error"
		met = false
		statuses = append(statuses, status)
		return
	}

	if csvVersionInfo.Compare(*serverVersion) > 0 {
		status.Status = v1alpha1.RequirementStatusReasonPresentNotSatisfied
		status.Message = fmt.Sprintf("CSV version requirement not met: minKubeVersion (%s) > server version (%s)", minKubeVersion, serverVersion.String())
		met = false
		statuses = append(statuses, status)
		return
	}
	status.Status = v1alpha1.RequirementStatusReasonPresent
	status.Message = fmt.Sprintf("CSV minKubeVersion (%s) less than server version (%s)", minKubeVersion, serverVersionInfo.String())
	met = true
	statuses = append(statuses, status)
	return
}

func (r *ClusterServiceVersionReconciler) requirementStatus(strategyDetailsDeployment *v1alpha1.StrategyDetailsDeployment, crdDescs []v1alpha1.CRDDescription,
	ownedAPIServiceDescs []v1alpha1.APIServiceDescription, requiredAPIServiceDescs []v1alpha1.APIServiceDescription,
	requiredNativeAPIs []metav1.GroupVersionKind) (met bool, statuses []v1alpha1.RequirementStatus) {
	met = true
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	config, err := k8sconfig.ClientConfig()

	// Retrieve server k8s version
	apiextensionsClient, err := apiextensions.NewForConfig(config)
	if err != nil {
		return
	}

	// Check for CRDs
	for _, r := range crdDescs {
		status := v1alpha1.RequirementStatus{
			Group:   "apiextensions.k8s.io",
			Version: "v1",
			Kind:    "CustomResourceDefinition",
			Name:    r.Name,
		}

		// check if CRD exists - this verifies group, version, and kind, so no need for GVK check via discovery
		crd, err := apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), r.Name, metav1.GetOptions{})
		if err != nil {
			status.Status = v1alpha1.RequirementStatusReasonNotPresent
			status.Message = "CRD is not present"
			log.Debugf("Setting 'met' to false, %v with status %v, with err: %v", r.Name, status, err)
			met = false
			statuses = append(statuses, status)
			continue
		}

		served := false
		for _, version := range crd.Spec.Versions {
			if version.Name == r.Version {
				if version.Served {
					served = true
				}
				break
			}
		}

		if !served {
			status.Status = v1alpha1.RequirementStatusReasonNotPresent
			status.Message = "CRD version not served"
			log.Debugf("Setting 'met' to false, %v with status %v, CRD version %v not found", r.Name, status, r.Version)
			met = false
			statuses = append(statuses, status)
			continue
		}

		// Check if CRD has successfully registered with k8s API
		established := false
		namesAccepted := false
		for _, cdt := range crd.Status.Conditions {
			switch cdt.Type {
			case apiextensionsv1.Established:
				if cdt.Status == apiextensionsv1.ConditionTrue {
					established = true
				}
			case apiextensionsv1.NamesAccepted:
				if cdt.Status == apiextensionsv1.ConditionTrue {
					namesAccepted = true
				}
			}
		}

		if established && namesAccepted {
			status.Status = v1alpha1.RequirementStatusReasonPresent
			status.Message = "CRD is present and Established condition is true"
			status.UUID = string(crd.GetUID())
			statuses = append(statuses, status)
		} else {
			status.Status = v1alpha1.RequirementStatusReasonNotAvailable
			status.Message = "CRD is present but the Established condition is False (not available)"
			met = false
			log.Debugf("Setting 'met' to false, %v with status %v, established=%v, namesAccepted=%v", r.Name, status, established, namesAccepted)
			statuses = append(statuses, status)
		}
	}

	apiRegistration, err := apiregistration.NewForConfig(config)
	if err != nil {
		return
	}

	// Check for required API services
	for _, required := range requiredAPIServiceDescs {
		name := fmt.Sprintf("%s.%s", required.Version, required.Group)
		status := v1alpha1.RequirementStatus{
			Group:   "apiregistration.k8s.io",
			Version: "v1",
			Kind:    "APIService",
			Name:    name,
		}

		// Check if GVK exists
		if err := r.isGVKRegistered(required.Group, required.Version, required.Kind); err != nil {
			status.Status = "NotPresent"
			met = false
			statuses = append(statuses, status)
			continue
		}

		// Check if APIService is registered
		apiService, err := apiRegistration.ApiregistrationV1().APIServices().Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			status.Status = "NotPresent"
			met = false
			statuses = append(statuses, status)
			continue
		}

		// Check if API is available
		if !IsAPIServiceAvailable(apiService) {
			status.Status = "NotPresent"
			met = false
		} else {
			status.Status = "Present"
			status.UUID = string(apiService.GetUID())
		}
		statuses = append(statuses, status)
	}

	// Check owned API services
	for _, r := range ownedAPIServiceDescs {
		name := fmt.Sprintf("%s.%s", r.Version, r.Group)
		status := v1alpha1.RequirementStatus{
			Group:   "apiregistration.k8s.io",
			Version: "v1",
			Kind:    "APIService",
			Name:    name,
		}

		found := false
		for _, spec := range strategyDetailsDeployment.DeploymentSpecs {
			if spec.Name == r.DeploymentName {
				status.Status = "DeploymentFound"
				statuses = append(statuses, status)
				found = true
				break
			}
		}

		if !found {
			status.Status = "DeploymentNotFound"
			statuses = append(statuses, status)
			met = false
		}
	}

	for _, required := range requiredNativeAPIs {
		name := fmt.Sprintf("%s.%s", required.Version, required.Group)
		status := v1alpha1.RequirementStatus{
			Group:   required.Group,
			Version: required.Version,
			Kind:    required.Kind,
			Name:    name,
		}

		if err := r.isGVKRegistered(required.Group, required.Version, required.Kind); err != nil {
			status.Status = v1alpha1.RequirementStatusReasonNotPresent
			status.Message = "Native API does not exist"
			met = false
			statuses = append(statuses, status)
			continue
		} else {
			status.Status = v1alpha1.RequirementStatusReasonPresent
			status.Message = "Native API exists"
			statuses = append(statuses, status)
			continue
		}
	}

	return
}

func (r *ClusterServiceVersionReconciler) isGVKRegistered(group, version, kind string) error {
	logger := r.Log.WithValues(log.Fields{
		"group":   group,
		"version": version,
		"kind":    kind,
	})
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	config, err := k8sconfig.ClientConfig()

	// Retrieve server k8s version
	kubernetesClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	gv := metav1.GroupVersion{Group: group, Version: version}
	resources, err := kubernetesClient.Discovery().ServerResourcesForGroupVersion(gv.String())
	if err != nil {
		logger.WithValues("err", err).Info("could not query for GVK in api discovery")
		return err
	}

	for _, r := range resources.APIResources {
		if r.Kind == kind {
			return nil
		}
	}

	logger.Info("couldn't find GVK in api discovery")
	return GroupVersionKindNotFoundError{group, version, kind}
}

func (r *ClusterServiceVersionReconciler) permissionStatus(strategyDetailsDeployment *v1alpha1.StrategyDetailsDeployment, ruleChecker RuleChecker, targetNamespace string, csv *v1alpha1.ClusterServiceVersion) (bool, []v1alpha1.RequirementStatus, error) {
	logger := log.WithFields(log.Fields{})
	statusesSet := map[string]v1alpha1.RequirementStatus{}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	config, err := k8sconfig.ClientConfig()
	if err != nil {
		logger.Error("CREAT K8S CONFIG ERR")
		return false, nil, err
	}
	// Retrieve server k8s version
	kubernetesClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Error("CREAT kubernetesClient ERR")
		return false, nil, err
	}

	checkPermissions := func(permissions []v1alpha1.StrategyDeploymentPermissions, namespace string) (bool, error) {
		met := true
		for _, perm := range permissions {
			saName := perm.ServiceAccountName
			var status v1alpha1.RequirementStatus
			if stored, ok := statusesSet[saName]; !ok {
				status = v1alpha1.RequirementStatus{
					Group:      "",
					Version:    "v1",
					Kind:       "ServiceAccount",
					Name:       saName,
					Status:     v1alpha1.RequirementStatusReasonPresent,
					Dependents: []v1alpha1.DependentStatus{},
				}
			} else {
				status = stored
			}

			// Ensure the ServiceAccount exists
			sa, err := kubernetesClient.CoreV1().ServiceAccounts(csv.GetNamespace()).Get(context.TODO(), perm.ServiceAccountName, metav1.GetOptions{})
			if err != nil {
				err, sa = r.createServiceAccount(csv, perm.ServiceAccountName)
				if err != nil {
					logger.Error("createServiceAccount ERR", err.Error())
					met = false
					status.Status = v1alpha1.RequirementStatusReasonNotPresent
					status.Message = "Service account does not exist"
					statusesSet[saName] = status
					continue
				}
			}
			// Check SA's ownership
			if ownerutil.IsOwnedByKind(sa, v1alpha1.ClusterServiceVersionKind) && !ownerutil.IsOwnedBy(sa, csv) {
				err := kubernetesClient.CoreV1().ServiceAccounts(csv.GetNamespace()).Delete(context.TODO(), perm.ServiceAccountName, metav1.DeleteOptions{})
				if err != nil {
					met = false
					continue
				}
				err, sa = r.createServiceAccount(csv, perm.ServiceAccountName)
				if err != nil {
					logger.Error("createServiceAccount ERR", err.Error())
					met = false
					status.Status = v1alpha1.RequirementStatusReasonNotPresent
					status.Message = "Service account does not exist"
					statusesSet[saName] = status
					continue
				}
			}

			// Check if PolicyRules are satisfied
			for _, rule := range perm.Rules {
				dependent := v1alpha1.DependentStatus{
					Group:   "rbac.authorization.k8s.io",
					Kind:    "PolicyRule",
					Version: "v1",
				}

				marshalled, err := json.Marshal(rule)
				if err != nil {
					logger.Error("marshalled error")
					dependent.Status = v1alpha1.DependentStatusReasonNotSatisfied
					dependent.Message = "rule unmarshallable"
					status.Dependents = append(status.Dependents, dependent)
					continue
				}

				var scope string
				if namespace == metav1.NamespaceAll {
					scope = "cluster"
				} else {
					scope = "namespaced"
				}
				dependent.Message = fmt.Sprintf("%s rule:%s", scope, marshalled)

				satisfied, err := ruleChecker.RuleSatisfied(sa, namespace, rule)
				if err != nil {
					return false, err
				} else if !satisfied {
					logger.Error("Unsatisfied")
					met = false
					dependent.Status = v1alpha1.DependentStatusReasonNotSatisfied
					status.Status = v1alpha1.RequirementStatusReasonPresentNotSatisfied
					status.Message = "Policy rule not satisfied for service account"
				} else {
					dependent.Status = v1alpha1.DependentStatusReasonSatisfied
				}

				status.Dependents = append(status.Dependents, dependent)
			}

			statusesSet[saName] = status
		}

		return met, nil
	}

	permMet, err := checkPermissions(strategyDetailsDeployment.Permissions, targetNamespace)
	if err != nil {
		return false, nil, err
	}
	clusterPermMet, err := checkPermissions(strategyDetailsDeployment.ClusterPermissions, metav1.NamespaceAll)
	if err != nil {
		return false, nil, err
	}

	for _, perm := range strategyDetailsDeployment.Permissions {
		sa, err := kubernetesClient.CoreV1().ServiceAccounts(csv.GetNamespace()).Get(context.TODO(), perm.ServiceAccountName, metav1.GetOptions{})
		if err != nil {
			return false, nil, err
		}
		err = creatRolesAndRolebindings(sa, perm.Rules)
		if err != nil {
			return false, nil, err
		}
	}

	for _, perm := range strategyDetailsDeployment.ClusterPermissions {
		sa, err := kubernetesClient.CoreV1().ServiceAccounts(csv.GetNamespace()).Get(context.TODO(), perm.ServiceAccountName, metav1.GetOptions{})
		if err != nil {
			return false, nil, err
		}
		err = creatClusterRolesAndClusterRolebindings(sa, perm.Rules)
		if err != nil {
			return false, nil, err
		}
	}

	statuses := []v1alpha1.RequirementStatus{}
	for key, status := range statusesSet {
		log.WithField("key", key).WithField("status", status).Tracef("appending permission status")
		statuses = append(statuses, status)
	}

	return permMet && clusterPermMet, statuses, nil
}

// IsObsolete returns if this CSV is being replaced or is marked for deletion

func (r *ClusterServiceVersionReconciler) installDeployment(csv *v1alpha1.ClusterServiceVersion, d *v1alpha1.StrategyDeploymentSpec) (*appsv1.Deployment, error) {
	kubeconfig := filepath.Join(
		os.Getenv("HOME"), ".kube", "config",
	)
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	dep := &appsv1.Deployment{Spec: d.Spec}

	dep.SetName(d.Name)
	dep.SetNamespace(csv.GetNamespace())

	// Merge annotations (to avoid losing info from pod template)
	annotations := map[string]string{}
	for k, v := range dep.Spec.Template.GetAnnotations() {
		annotations[k] = v
	}
	for k, v := range csv.GetAnnotations() {
		annotations[k] = v
	}
	dep.Spec.Template.SetAnnotations(annotations)

	revisionHistoryLimit := int32(1)
	dep.Spec.RevisionHistoryLimit = &revisionHistoryLimit

	_, err = clientset.AppsV1().Deployments(dep.Namespace).Create(context.TODO(), dep, metav1.CreateOptions{})
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			return nil, err
		}
	}
	return dep, nil
}

func (r *ClusterServiceVersionReconciler) handleCSVDeletion(reqLogger logr.Logger, csv *v1alpha1.ClusterServiceVersion) error {
	if err := r.cleanupCSVDeployments(reqLogger, csv); err != nil {
		reqLogger.Error(err, "failed to delete strategy")
		return nil
	}
	if err := r.deleteServiceAccount(csv, csv.Name); err != nil {
		reqLogger.Error(err, "failed to delete ServiceAccount")
		return nil
	}
	err := r.Client.Delete(context.TODO(), csv)
	if err != nil {
		return nil
	}
	return nil
}

func (r *ClusterServiceVersionReconciler) cleanupCSVDeployments(reqlogger logr.Logger, csv *v1alpha1.ClusterServiceVersion) error {
	// Extract the InstallStrategy for the deployment
	strategy, err := r.UnmarshalStrategy(csv)
	if err != nil {
		reqlogger.Error(err, "could not parse install strategy while cleaning up CSV deployment")
		return err
	}

	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		reqlogger.Error(err, "could not cast install strategy as type %T", strategyDetailsDeployment)
		return err
	}
	kubeconfig := filepath.Join(
		os.Getenv("HOME"), ".kube", "config",
	)
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		reqlogger.Error(err, err.Error())
		return err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		reqlogger.Error(err, err.Error())
		return err
	}

	// Delete deployments
	for _, spec := range strategyDetailsDeployment.DeploymentSpecs {
		if err := clientset.AppsV1().Deployments(csv.Namespace).Delete(context.TODO(), spec.Name, metav1.DeleteOptions{}); err != nil {
			if k8serrors.IsNotFound(err) {
				return nil
			}
			return err
		}
	}
	return nil
}

func (r *ClusterServiceVersionReconciler) createOrUpdateCertResourcesForDeployment(csv *v1alpha1.ClusterServiceVersion) error {
	for _, desc := range r.getCertResources(csv) {
		switch d := desc.(type) {
		case *apiServiceDescriptionsWithCAPEM:
			err := r.createOrUpdateAPIService(csv, d.caPEM, d.apiServiceDescription)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("Unsupported CA Resource")
		}
	}
	return nil
}

// Apply runs series of overrides functions that will properly initialize
// the deployment object.
func (c DeploymentInitializerFuncChain) Apply(deployment *appsv1.Deployment) (err error) {
	for _, initializer := range c {
		if initializer == nil {
			continue
		}

		if initializationErr := initializer(deployment); initializationErr != nil {
			err = initializationErr
			break
		}
	}
	return
}

func (r *ClusterServiceVersionReconciler) UnmarshalStrategy(csv *v1alpha1.ClusterServiceVersion) (strategy Strategy, err error) {
	s := csv.Spec.InstallStrategy
	switch s.StrategyName {
	case v1alpha1.InstallStrategyNameDeployment:
		return &s.StrategySpec, nil
	}
	err = fmt.Errorf("unrecognized install strategy")
	return
}

const TimedOutReason = "ProgressDeadlineExceeded"

// Status returns a message describing deployment status, and a bool value indicating if the status is considered done.
func DeploymentStatus(deployment *appsv1.Deployment) (string, bool, error) {
	if deployment.Generation <= deployment.Status.ObservedGeneration {
		// check if deployment has timed out
		cond := getDeploymentCondition(deployment.Status, appsv1.DeploymentProgressing)
		if cond != nil && cond.Reason == TimedOutReason {
			return "", false, fmt.Errorf("deployment %q exceeded its progress deadline", deployment.Name)
		}
		// not all replicas are up yet
		if deployment.Spec.Replicas != nil && deployment.Status.UpdatedReplicas < *deployment.Spec.Replicas {
			return fmt.Sprintf("Waiting for rollout to finish: %d out of %d new replicas have been updated...\n", deployment.Status.UpdatedReplicas, *deployment.Spec.Replicas), false, nil
		}
		// waiting for old replicas to be cleaned up
		if deployment.Status.Replicas > deployment.Status.UpdatedReplicas {
			return fmt.Sprintf("Waiting for rollout to finish: %d old replicas are pending termination...\n", deployment.Status.Replicas-deployment.Status.UpdatedReplicas), false, nil
		}
		// waiting for new replicas to report as available
		if deployment.Status.AvailableReplicas < deployment.Status.UpdatedReplicas {
			return fmt.Sprintf("Waiting for rollout to finish: %d of %d updated replicas are available...\n", deployment.Status.AvailableReplicas, deployment.Status.UpdatedReplicas), false, nil
		}
		// deployment is finished
		return fmt.Sprintf("deployment %q successfully rolled out\n", deployment.Name), true, nil
	}
	return fmt.Sprintf("Waiting for deployment spec update to be observed...\n"), false, nil
}

func getDeploymentCondition(status appsv1.DeploymentStatus, condType appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == condType {
			return &c
		}
	}
	return nil
}

func (r *ClusterServiceVersionReconciler) createServiceAccount(csv *v1alpha1.ClusterServiceVersion, name string) (error, *corev1.ServiceAccount) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	config, err := k8sconfig.ClientConfig()
	if err != nil {
		return err, nil
	}
	kubernetesClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err, nil
	}
	//defaultSA, err := kubernetesClient.CoreV1().ServiceAccounts(csv.GetNamespace()).Get(context.TODO(), "default", metav1.GetOptions{})
	//var secrets []corev1.LocalObjectReference
	blockOwnerDeletion := true
	isController := true
	//mysecret := []string{""}
	//for _, secretName := range mysecret {
	//	secrets = append(secrets, corev1.LocalObjectReference{Name: secretName})
	//}
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: csv.GetNamespace(),
			OwnerReferences: []metav1.OwnerReference{
				{
					Name:               csv.Name,
					Kind:               v1alpha1.ClusterServiceVersionKind,
					APIVersion:         v1alpha1.ClusterServiceVersionAPIVersion,
					UID:                csv.GetUID(),
					Controller:         &isController,
					BlockOwnerDeletion: &blockOwnerDeletion,
				},
			},
		},
		// ImagePullSecrets: secrets,
	}

	saCreated, err := kubernetesClient.CoreV1().ServiceAccounts(csv.Namespace).Create(context.TODO(), sa, metav1.CreateOptions{})
	if err != nil {
		return err, nil
	}
	return nil, saCreated
}

func (r *ClusterServiceVersionReconciler) deleteServiceAccount(csv *v1alpha1.ClusterServiceVersion, name string) error {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	config, err := k8sconfig.ClientConfig()
	if err != nil {
		return err
	}
	kubernetesClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	saCreated, err := kubernetesClient.CoreV1().ServiceAccounts(csv.Namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if saCreated != nil {
		if err := kubernetesClient.CoreV1().ServiceAccounts(csv.Namespace).Delete(context.TODO(), name, metav1.DeleteOptions{}); err != nil {
			if k8serrors.IsNotFound(err) {
				return nil
			}
			return err
		}
	}
	return nil
}

func creatRolesAndRolebindings(account *corev1.ServiceAccount, rules []rbacv1.PolicyRule) error {
	// create Role and RoleBinding to allow the deployment to mount the pods
	logger := log.WithFields(log.Fields{})
	for _, rule := range rules {
		k8srules := clientcmd.NewDefaultClientConfigLoadingRules()
		k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(k8srules, &clientcmd.ConfigOverrides{})
		config, err := k8sconfig.ClientConfig()
		if err != nil {
			return err
		}
		kubernetesClient, err := kubernetes.NewForConfig(config)
		Role := &rbacv1.Role{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:         rule.Verbs,
					APIGroups:     rule.APIGroups,
					Resources:     rule.Resources,
					ResourceNames: rule.ResourceNames,
				},
			},
		}
		Role.SetName(account.GetName())
		Role.SetNamespace(account.GetNamespace())

		existingRole, err := kubernetesClient.RbacV1().Roles(account.GetNamespace()).Get(context.TODO(), Role.Name, metav1.GetOptions{})
		if err == nil {
			// Attempt an update
			if _, err := kubernetesClient.RbacV1().Roles(account.GetNamespace()).Update(context.TODO(), existingRole, metav1.UpdateOptions{}); err != nil {
				logger.Warnf("could not update secret role %s", Role.GetName())
				return err
			}
		} else if k8serrors.IsNotFound(err) {
			// Create the role
			_, err = kubernetesClient.RbacV1().Roles(account.GetNamespace()).Create(context.TODO(), Role, metav1.CreateOptions{})
			if err != nil {
				log.Warnf("could not create secret role %s", Role.GetName())
				return err
			}
		} else {
			return err
		}

		RoleBinding := &rbacv1.RoleBinding{
			Subjects: []rbacv1.Subject{
				{
					Kind:      rbacv1.ServiceAccountKind,
					APIGroup:  "",
					Name:      account.GetName(),
					Namespace: account.GetNamespace(),
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     Role.GetName(),
			},
		}
		RoleBinding.SetName(account.GetName())
		RoleBinding.SetNamespace(account.GetNamespace())

		_, err = kubernetesClient.RbacV1().RoleBindings(account.GetNamespace()).Get(context.TODO(), RoleBinding.Name, metav1.GetOptions{})
		if err == nil {
			// Attempt an update
			if _, err := kubernetesClient.RbacV1().RoleBindings(account.GetNamespace()).Update(context.TODO(), RoleBinding, metav1.UpdateOptions{}); err != nil {
				logger.Warnf("could not update rolebinding %s", RoleBinding.GetName())
				return err
			}
		} else if k8serrors.IsNotFound(err) {
			// Create the role
			_, err = kubernetesClient.RbacV1().RoleBindings(account.GetNamespace()).Create(context.TODO(), RoleBinding, metav1.CreateOptions{})
			if err != nil {
				log.Warnf("could not create rolebinding")
				return err
			}
		} else {
			return err
		}
	}
	return nil
}

func creatClusterRolesAndClusterRolebindings(account *corev1.ServiceAccount, rules []rbacv1.PolicyRule) error {
	// create Role and RoleBinding to allow the deployment to mount the pods
	logger := log.WithFields(log.Fields{})
	for _, rule := range rules {
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
		config, err := k8sconfig.ClientConfig()
		if err != nil {
			return err
		}
		kubernetesClient, err := kubernetes.NewForConfig(config)
		if err != nil {
			return err
		}
		ClusterRole := &rbacv1.ClusterRole{
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:         rule.Verbs,
					APIGroups:     rule.APIGroups,
					Resources:     rule.Resources,
					ResourceNames: rule.ResourceNames,
				},
			},
		}
		ClusterRole.SetName(account.GetName())
		ClusterRole.SetNamespace(account.GetNamespace())

		existingRole, err := kubernetesClient.RbacV1().ClusterRoles().Get(context.TODO(), ClusterRole.Name, metav1.GetOptions{})
		if err == nil {
			// Attempt an update
			if _, err := kubernetesClient.RbacV1().ClusterRoles().Update(context.TODO(), existingRole, metav1.UpdateOptions{}); err != nil {
				logger.Warnf("could not update role %s", ClusterRole.GetName())
				return err
			}
		} else if k8serrors.IsNotFound(err) {
			// Create the role
			_, err = kubernetesClient.RbacV1().ClusterRoles().Create(context.TODO(), ClusterRole, metav1.CreateOptions{})
			if err != nil {
				log.Warnf("could not create role %s", ClusterRole.GetName())
				return err
			}
		} else {
			return err
		}

		ClusterRoleBinding := &rbacv1.ClusterRoleBinding{
			Subjects: []rbacv1.Subject{
				{
					Kind:      rbacv1.ServiceAccountKind,
					APIGroup:  "",
					Name:      account.GetName(),
					Namespace: account.GetNamespace(),
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     ClusterRole.GetName(),
			},
		}
		ClusterRoleBinding.SetName(account.GetName())
		ClusterRoleBinding.SetNamespace(account.GetNamespace())

		_, err = kubernetesClient.RbacV1().ClusterRoleBindings().Get(context.TODO(), ClusterRoleBinding.Name, metav1.GetOptions{})
		if err == nil {
			// Attempt an update
			if _, err := kubernetesClient.RbacV1().ClusterRoleBindings().Update(context.TODO(), ClusterRoleBinding, metav1.UpdateOptions{}); err != nil {
				logger.Warnf("could not update rolebinding %s", ClusterRoleBinding.GetName())
				return err
			}
		} else if k8serrors.IsNotFound(err) {
			// Create the role
			_, err = kubernetesClient.RbacV1().ClusterRoleBindings().Create(context.TODO(), ClusterRoleBinding, metav1.CreateOptions{})
			if err != nil {
				log.Warnf("could not create rolebinding")
				return err
			}
		} else {
			return err
		}
	}
	return nil
}

func now() *metav1.Time {
	now := metav1.NewTime(utilclock.RealClock{}.Now().UTC())
	return &now
}

func (r *ClusterServiceVersionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ClusterServiceVersion{}).
		Complete(r)
}
