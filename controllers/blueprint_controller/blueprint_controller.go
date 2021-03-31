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
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
	"gopkg.in/yaml.v2"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	yamlDecoder "k8s.io/apimachinery/pkg/util/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	utilclock "k8s.io/apimachinery/pkg/util/clock"

	operatorv1 "github.com/buptGophers/operator-manager/api/v1"
	v1alpha1 "github.com/buptGophers/operator-manager/api/v1alpha1"
)

// BluePrintReconciler reconciles a BluePrint object
type BluePrintReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

type notSupportedStepperErr struct {
	message string
}

func (n notSupportedStepperErr) Error() string {
	return n.message
}

// +kubebuilder:rbac:groups=operator.operator-manager.domain,resources=blueprints/status,verbs=get;update;patch
func (r *BluePrintReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	reqLogger := r.Log.WithValues("Attempting to install!", req.NamespacedName)

	blueprintList := &operatorv1.BluePrintList{}
	err := r.Client.List(context.TODO(), blueprintList)
	if err != nil {
		if err := client.IgnoreNotFound(err); err != nil {
			reqLogger.Info("Cannot found any BluePrint!")
			return ctrl.Result{}, nil
		}
		reqLogger.Error(err, "Unexpected error!")
		return ctrl.Result{}, err
	}

	for _, blueprint := range blueprintList.Items {
		if blueprint.Spec.ClusterServiceVersion == "" {
			//there is a wrong blueprint need to delete?
			return ctrl.Result{}, err
		}
		// Get operator path for related CSV and CRDs
		ClusterServiceVersion := blueprint.Spec.ClusterServiceVersion
		path := r.getRelatedReference(ClusterServiceVersion)
		filepathNames, err := filepath.Glob(filepath.Join(path, "*"))
		if err != nil {
			return ctrl.Result{}, err
		}

		switch blueprint.Status.Plan.Status {
		case operatorv1.StepStatusDelete:
			// Uninstall the operator in the subscribed namespace
			csvList := &v1alpha1.ClusterServiceVersionList{}
			err := r.Client.List(context.TODO(), csvList)
			if err != nil {
				if err := client.IgnoreNotFound(err); err == nil {
					reqLogger.Info("185: Cannot found any ClusterServiceVersion in ", req.NamespacedName)
					return ctrl.Result{}, nil
				}
				reqLogger.Error(err, "Unexpected error!")
				return ctrl.Result{}, err
			}
			for _, path := range filepathNames {
				res, err := r.ParsingYaml(path)
				if res == "" && err != nil {
					return ctrl.Result{}, err
				} else if res == "" && err == nil {
					reqLogger.Info("Can't parse yaml file correctly!")
					return ctrl.Result{}, nil
				}
				switch res {
				case "crdV1":
					err = r.deleteCRDV1(path)
					if err != nil {
						return ctrl.Result{}, err
					}
				case "crdV1Beta1":
					err = r.deleteCRDV1Beta1(path)
					if err != nil {
						return ctrl.Result{}, err
					}
				case "csvV1alpha1":
					err := r.deleteClusterServiceVersionV1alpha1(reqLogger, blueprint.Spec.ClusterServiceVersion)
					if err != nil {
						return ctrl.Result{}, err
					}
				default:
					reqLogger.Info("csv version")
				}
			}
			if err := r.Client.Delete(context.TODO(), &blueprint); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil

		case operatorv1.StepStatusPresent:
			// Need to uninstall operator and its resources before update
			if r.deleteClusterServiceVersionV1alpha1(reqLogger, blueprint.Spec.OldVersion) != nil {
				return ctrl.Result{}, err
			}
			oldVersionPath := r.getRelatedReference(blueprint.Spec.OldVersion)
			oldVersionPathNames, err := filepath.Glob(filepath.Join(oldVersionPath, "*"))
			if err != nil {
				return ctrl.Result{}, err
			}
			for _, path := range oldVersionPathNames {
				res, err := r.ParsingYaml(path)
				if err != nil {
					return ctrl.Result{}, err
				}
				if res == "" {
					return ctrl.Result{}, nil
				}
				switch res {
				case "crdV1":
					err = r.deleteCRDV1(path)
					if err != nil {
						return ctrl.Result{}, err
					}
				case "crdV1Beta1":
					err = r.deleteCRDV1Beta1(path)
					if err != nil {
						return ctrl.Result{}, err
					}
				}
			}
			if r.deleteOldVersion(reqLogger, blueprint.Spec.OldVersion) != nil {
				return ctrl.Result{}, err
			}
			blueprint.Status.Plan.Status = operatorv1.StepStatusUnknown
			fallthrough

		case operatorv1.StepStatusUnknown:
			// Install operator and apply its resources
			blueprint.Status.Plan.Status = operatorv1.StepStatusCreated
			if err := r.Client.Update(context.TODO(), &blueprint); err != nil {
				reqLogger.Info("_____________________Update error!")
				return ctrl.Result{}, err
			}

			for _, f := range filepathNames {
				res, err := r.ParsingYaml(f)
				if res == "" && err != nil {
					return ctrl.Result{}, err
				} else if res == "" && err == nil {
					reqLogger.Info("Can't parse yaml file correctly!")
					return ctrl.Result{}, nil
				}
				switch res {
				case "crdV1":
					err = r.createCRDV1(f)
					if err != nil {
						return ctrl.Result{}, err
					}
				case "crdV1Beta1":
					err = r.createCRDV1Beta1(f)
					if err != nil {
						return ctrl.Result{}, err
					}
				//case "csvV1alpha1":
				// _, err := r.createClusterServiceVersionV1alpha1(reqLogger, &blueprint.Status.Plan, path, &blueprint)
				// if err != nil {
				//    return ctrl.Result{}, err
				// }
				default:
					break
				}
			}

			for _, f := range filepathNames {
				res, err := r.ParsingYaml(f)
				if res == "" && err != nil {
					return ctrl.Result{}, err
				} else if res == "" && err == nil {
					reqLogger.Info("Can't parse yaml file correctly!")
					return ctrl.Result{}, nil
				}
				if res == "csvV1alpha1" {
					_, err := r.createClusterServiceVersionV1alpha1(reqLogger, &blueprint.Status.Plan, path, &blueprint)
					if err != nil {
						return ctrl.Result{}, err
					}
				}
			}

			reqLogger.Info("Successfully create subscribed CSV and CRDs!")

		case operatorv1.StepStatusCreated:
			break

		default:
			reqLogger.Info("Unexpected status for blueprint")
		}
	}
	return ctrl.Result{}, nil
}

func (r *BluePrintReconciler) createClusterServiceVersionV1alpha1(reqLogger logr.Logger, step *operatorv1.Step, path string, blueprint *operatorv1.BluePrint) (*v1alpha1.ClusterServiceVersion, error) {
	csv, err := r.findCSV(*step, path)
	if err != nil {
		return nil, err
	}
	csv.SetNamespace(blueprint.Namespace)

	csvList := &v1alpha1.ClusterServiceVersionList{}
	err = r.Client.List(context.TODO(), csvList)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Info("CSV resource is not found.")
			err = r.Client.Create(context.TODO(), csv)
			if err != nil {
				reqLogger.Info("1, Unexpected err while create csv")
				return nil, err
			}
			return csv, nil
		}
		reqLogger.Error(err, "Failed to get csv.")
		return nil, err
	}
	for _, c := range csvList.Items {
		if c.Name == csv.Name {
			return &c, nil
		}
	}
	err = r.Client.Create(context.TODO(), csv)
	if err != nil {
		reqLogger.Info("2, Unexpected err while create csv")
		return nil, err
	}
	return csv, nil
}

func (r *BluePrintReconciler) deleteClusterServiceVersionV1alpha1(reqLogger logr.Logger, name string) error {
	csvList := &v1alpha1.ClusterServiceVersionList{}
	err := r.Client.List(context.TODO(), csvList)
	if err != nil {
		if errors.IsNotFound(err) {
			reqLogger.Info("CSV resource is not found.")
			return nil
		}
		reqLogger.Error(err, "Failed to list csv.")
		return err
	}
	for _, c := range csvList.Items {
		if name[:4] == c.Name[:4] && name[len(name)-4:] == c.Name[len(c.Name)-4:] {
			c.SetPhase(v1alpha1.CSVPhaseDeleting, v1alpha1.CSVReasonReplaced, "delete clusterserviceversion", now())
			if r.Client.Update(context.TODO(), &c) != nil {
				return err
			}
			return nil
		}
	}
	return nil
}

func (r *BluePrintReconciler) deleteOldVersion(reqLogger logr.Logger, oldVersionName string) error {
	blueprintList := &operatorv1.BluePrintList{}
	err := r.Client.List(context.TODO(), blueprintList)
	if err != nil {
		if err := client.IgnoreNotFound(err); err != nil {
			reqLogger.Info("No bluePrint in cluster!")
			return nil
		}
		return err
	}
	for _, blueprint := range blueprintList.Items {
		if blueprint.Spec.ClusterServiceVersion == oldVersionName {
			if err := r.Client.Delete(context.TODO(), &blueprint); err != nil {
				return err
			}
			return nil
		}
	}
	return nil
}

func (r *BluePrintReconciler) createCRDV1(path string) error {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	err := DecodeFile(path, crd)
	if err != nil {
		return err
	}
	apiExtensionsClient, err := NewApiExtensionClinet()
	if err != nil {
		return err
	}
	_, err = apiExtensionsClient.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crd.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, createError := apiExtensionsClient.ApiextensionsV1().CustomResourceDefinitions().Create(context.TODO(), crd, metav1.CreateOptions{})
			if createError != nil {
				return createError
			}
			return nil
		}
		return err
	}
	return nil
}

func (r *BluePrintReconciler) deleteCRDV1(path string) error {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	err := DecodeFile(path, crd)
	if err != nil {
		return err
	}
	apiExtensionsClient, err := NewApiExtensionClinet()
	if err != nil {
		return err
	}

	_, err = apiExtensionsClient.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crd.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	deleteError := apiExtensionsClient.ApiextensionsV1().CustomResourceDefinitions().Delete(context.TODO(), crd.Name, metav1.DeleteOptions{})
	if deleteError != nil {
		return deleteError
	}
	return nil
}

func (r *BluePrintReconciler) createCRDV1Beta1(path string) error {
	crd := &apiextensionsv1beta1.CustomResourceDefinition{}
	err := DecodeFile(path, crd)
	if err != nil {
		return err
	}
	apiExtensionsClient, err := NewApiExtensionClinet()
	if err != nil {
		return err
	}
	_, err = apiExtensionsClient.ApiextensionsV1beta1().CustomResourceDefinitions().Get(context.TODO(), crd.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, createError := apiExtensionsClient.ApiextensionsV1beta1().CustomResourceDefinitions().Create(context.TODO(), crd, metav1.CreateOptions{})
			if createError != nil {
				return createError
			}
			return nil
		}
		return err
	}
	return nil
}

func (r *BluePrintReconciler) deleteCRDV1Beta1(path string) error {
	crd := &apiextensionsv1beta1.CustomResourceDefinition{}
	err := DecodeFile(path, crd)
	if err != nil {
		return err
	}
	apiExtensionsClient, err := NewApiExtensionClinet()
	if err != nil {
		return err
	}

	_, err = apiExtensionsClient.ApiextensionsV1beta1().CustomResourceDefinitions().Get(context.TODO(), crd.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	deleteError := apiExtensionsClient.ApiextensionsV1beta1().CustomResourceDefinitions().Delete(context.TODO(), crd.Name, metav1.DeleteOptions{})
	if deleteError != nil {
		return deleteError
	}
	return nil
}

// Stepper manages cluster interactions based on the step.
type Stepper interface {
	Status() (operatorv1.StepStatus, error)
}

func (r *BluePrintReconciler) getRelatedReference(ClusterServiceVersion string) string {
	operator, version := SplitStartingCSV(ClusterServiceVersion)
	return "./config/bundles/" + operator + "/" + version
}

func (r *BluePrintReconciler) ParsingYaml(path string) (string, error) {
	resultMap := make(map[string]interface{})
	yamlFile, err := ioutil.ReadFile(path)
	if err != nil {
		return "", err
	}
	err = yaml.Unmarshal(yamlFile, resultMap)
	if err != nil {
		return "", err
	}
	switch resultMap["kind"] {
	case crdKind:
		version := resultMap["apiVersion"]
		switch version {
		case "apiextensions.k8s.io/v1":
			return "crdV1", nil
		case "apiextensions.k8s.io/v1beta1":
			return "crdV1Beta1", nil
		}
	case csvKind:
		return "csvV1alpha1", nil
	}
	return "", nil
}

func (r *BluePrintReconciler) findCSV(step operatorv1.Step, path string) (*v1alpha1.ClusterServiceVersion, error) {
	filepathNames, err := filepath.Glob(filepath.Join(path, "*"))
	if err != nil {
		return nil, err
	}
	resultMap := make(map[string]interface{})
	for i := range filepathNames {
		yamlFile, err := ioutil.ReadFile(filepathNames[i])
		if err != nil {
			return nil, err
		}
		err = yaml.Unmarshal(yamlFile, resultMap)
		switch resultMap["kind"] {
		case csvKind:
			version := resultMap["apiVersion"]
			if err != nil {
				return nil, err
			}
			switch version {
			case "operators.coreos.com/v1alpha1":
				csv := &v1alpha1.ClusterServiceVersion{}
				err = DecodeFile(filepathNames[i], csv)
				csv1 := &v1alpha1.ClusterServiceVersion{
					TypeMeta:   csv.TypeMeta,
					ObjectMeta: csv.ObjectMeta,
					Spec:       csv.Spec,
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhasePending,
					},
				}
				if err != nil {
					return nil, err
				}
				return csv1, nil
			default:
				return nil, notSupportedStepperErr{fmt.Sprintf("stepper interface does not support %s", step.Resource.Kind)}
			}
		}
	}
	return nil, notSupportedStepperErr{fmt.Sprintf("stepper interface does not support %s", step.Resource.Kind)}
}

// StepperFunc fulfills the Stepper interface.
type StepperFunc func() (operatorv1.StepStatus, error)

func (s StepperFunc) Status() (operatorv1.StepStatus, error) {
	return s()
}

// DecodeFile decodes the file at a path into the given interface.
func DecodeFile(path string, into interface{}) error {
	if into == nil {
		panic("programmer error: decode destination must be instantiated before decode")
	}

	fileReader, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("unable to read file %s: %s", path, err)
	}
	defer fileReader.Close()

	decoder := yamlDecoder.NewYAMLOrJSONDecoder(fileReader, 30)
	return decoder.Decode(into)
}

func now() *metav1.Time {
	now := metav1.NewTime(utilclock.RealClock{}.Now().UTC())
	return &now
}

func SplitStartingCSV(StartingCSV string) (string, string) {
	return StartingCSV[:strings.IndexAny(StartingCSV, ".")], StartingCSV[strings.IndexAny(StartingCSV, ".")+1:]
}

func (r *BluePrintReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorv1.BluePrint{}).
		Complete(r)
}
