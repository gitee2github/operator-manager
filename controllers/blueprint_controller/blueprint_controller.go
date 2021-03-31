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
	"gopkg.in/yaml.v2"
	"io/ioutil"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	yamlDecoder "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operatorv1 "github.com/buptGophers/operator-manager/api/v1"
	v1alpha1 "github.com/buptGophers/operator-manager/api/v1alpha1"
)

// BluePrintReconciler reconciles a BluePrint object
type BluePrintReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
	//manifestResolver  ManifestResolver
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
	// Get subscription
	reqLogger := r.Log.WithValues("attempting to install", req.Namespace)
	// List BluePrint
	blueprintList := &operatorv1.BluePrintList{}
	err := r.Client.List(context.TODO(), blueprintList)
	if err != nil {
		if err := client.IgnoreNotFound(err); err != nil {
			reqLogger.Info("Cannot found any BluePrint in ", req.Namespace)
			return ctrl.Result{}, nil
		}
		reqLogger.Error(err, "unexpected error!")
		return ctrl.Result{}, err
	}
	if len(blueprintList.Items) != 0 {
		for _, blueprint := range blueprintList.Items {
			if len(blueprint.Spec.ClusterServiceVersionNames) == 0 {
				//there is a wrong blueprint need to delete?
				return ctrl.Result{}, err
			}
			for i, step := range blueprint.Status.Plan {
				//find path
				ClusterServiceVersionNames := blueprint.Spec.ClusterServiceVersionNames[0]
				path := r.getRelatedReference(ClusterServiceVersionNames)
				manifests := filepath.Join(path, "/manifests")
				filepathNames, err := filepath.Glob(filepath.Join(manifests, "*"))
				if err != nil {
					return ctrl.Result{}, err
				}
				switch step.Status {
				case operatorv1.StepStatusCreated:
					continue
				case operatorv1.StepStatusUnknown:
					for i := range filepathNames {
						res, err := r.ParsingYaml(filepathNames[i])
						if res == "" && err != nil {
							return ctrl.Result{}, err
						} else if res == "" && err == nil {
							reqLogger.Info("Can't parse yaml file correctly!")
							return ctrl.Result{}, nil
						}
						switch res {
						case "crdV1":
							err = r.createCRDV1(filepathNames[i])
							if err != nil {
								return ctrl.Result{}, err
							}
						case "crdV1Beta1":
							err = r.createCRDV1Beta1(filepathNames[i])
							if err != nil {
								return ctrl.Result{}, err
							}
						case "csvV1alpha1":
							_, err := r.createClusterServiceVersionV1alpha1(step, manifests, &blueprint)
							if err != nil {
								return ctrl.Result{}, err
							}
						}
					}
					blueprint.Status.Plan[i].Status = operatorv1.StepStatusCreated
					if err := r.Client.Update(context.TODO(), &blueprint); err != nil {
						return ctrl.Result{}, err
					}
				case operatorv1.StepStatusDelete:
					for i := range filepathNames {
						res, err := r.ParsingYaml(filepathNames[i])
						if res == "" && err != nil {
							return ctrl.Result{}, err
						} else if res == "" && err == nil {
							reqLogger.Info("Can't parse yaml file correctly!")
							return ctrl.Result{}, nil
						}
						switch res {
						case "crdV1":
							err = r.deleteCRDV1(filepathNames[i])
							if err != nil {
								return ctrl.Result{}, err
							}
						case "crdV1Beta1":
							err = r.deleteCRDV1Beta1(filepathNames[i])
							if err != nil {
								return ctrl.Result{}, err
							}
						case "csvV1alpha1":
							err = r.deleteClusterServiceVersionV1alpha1(step, manifests, &blueprint)
							if err != nil {
								return ctrl.Result{}, err
							}
						}
					}
					if err := r.Client.Delete(context.TODO(), &blueprint); err != nil {
						return ctrl.Result{}, err
					}
					reqLogger.Info("Delete a blueprint successful!")
				default:
					reqLogger.Info("Blueprint status belongs to an undefined state!")
					return ctrl.Result{}, err
				}
			}
		}
	}
	return ctrl.Result{}, nil
}

func (r *BluePrintReconciler) createClusterServiceVersionV1alpha1(step *operatorv1.Step, path string, blueprint *operatorv1.BluePrint) (*v1alpha1.ClusterServiceVersion, error) {
	csv := &v1alpha1.ClusterServiceVersion{}
	csv, err := r.findCSV(*step, path)
	if err != nil {
		return nil, err
	}
	csv.SetNamespace(blueprint.Namespace)
	err = r.Client.Create(context.TODO(), csv)
	if err != nil {
		return nil, err
	}
	return csv, nil
}

func (r *BluePrintReconciler) deleteClusterServiceVersionV1alpha1(step *operatorv1.Step, path string, blueprint *operatorv1.BluePrint) error {
	csv := &v1alpha1.ClusterServiceVersion{}
	csv, err := r.findCSV(*step, path)
	if err != nil {
		return err
	}
	csv.SetNamespace(blueprint.Namespace)
	err = r.Client.Delete(context.TODO(), csv)
	if err != nil {
		return err
	}
	return nil
}

func (r *BluePrintReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorv1.BluePrint{}).
		Complete(r)
}

func (r *BluePrintReconciler) createCRDV1(path string) error {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	err := DecodeFile(path, crd)
	if err != nil {
		return err
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	config, err := k8sconfig.ClientConfig()
	if err != nil {
		return err
	}
	apiextensionsClient, err := apiextensions.NewForConfig(config)
	_, createError := apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Create(context.TODO(), crd, metav1.CreateOptions{})
	if createError != nil {
		return createError
	}
	return nil
}

func (r *BluePrintReconciler) deleteCRDV1(path string) error {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	err := DecodeFile(path, crd)
	if err != nil {
		return err
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	config, err := k8sconfig.ClientConfig()
	if err != nil {
		return err
	}
	apiextensionsClient, err := apiextensions.NewForConfig(config)
	createError := apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Delete(context.TODO(), crd.Name, metav1.DeleteOptions{})
	if createError != nil {
		return createError
	}
	return nil
}

func (r *BluePrintReconciler) createCRDV1Beta1(path string) error {
	crd := &apiextensionsv1beta1.CustomResourceDefinition{}
	err := DecodeFile(path, crd)
	if err != nil {
		return err
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	config, err := k8sconfig.ClientConfig()
	if err != nil {
		return err
	}
	apiextensionsClient, err := apiextensions.NewForConfig(config)
	_, createError := apiextensionsClient.ApiextensionsV1beta1().CustomResourceDefinitions().Create(context.TODO(), crd, metav1.CreateOptions{})
	if createError != nil {
		return createError
	}
	return nil
}

func (r *BluePrintReconciler) deleteCRDV1Beta1(path string) error {
	crd := &apiextensionsv1beta1.CustomResourceDefinition{}
	err := DecodeFile(path, crd)
	if err != nil {
		fmt.Println("////////////////////////////1")
		return err
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	k8sconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	config, err := k8sconfig.ClientConfig()
	if err != nil {
		fmt.Println("////////////////////////////2")
		return err
	}
	apiextensionsClient, err := apiextensions.NewForConfig(config)
	deleteError := apiextensionsClient.ApiextensionsV1beta1().CustomResourceDefinitions().Delete(context.TODO(), crd.Name, metav1.DeleteOptions{})
	if deleteError != nil {
		fmt.Println("////////////////////////////3")
		return deleteError
	}
	return nil
}

// Stepper manages cluster interactions based on the step.
type Stepper interface {
	Status() (operatorv1.StepStatus, error)
}

func (r *BluePrintReconciler) getRelatedReference(ClusterServiceVersionNames string) string {
	return "./config/bundles/" + ClusterServiceVersionNames
}

func (r *BluePrintReconciler) ParsingYaml(path string) (string,error) {
	resultMap := make(map[string]interface{})
	yamlFile, err := ioutil.ReadFile(path)
	if err != nil {
		fmt.Println("readfile wrong!!!")
		return "", err
	}
	err = yaml.Unmarshal(yamlFile, resultMap)
	if err != nil {
		fmt.Println("yaml.Unmarshal wrong!!!")
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
				if err != nil {
					fmt.Println(err.Error())
					return nil, err
				}
				return csv, nil
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
