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
	operatorv1 "github.com/buptGophers/operator-manager/api/v1"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

// SubscriptionReconciler reconciles a Subscription object
type SubscriptionReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=operator.operator.domain,resources=subscriptions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operator.operator.domain,resources=subscriptions/status,verbs=get;update;patch

func (r *SubscriptionReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	// Get subscription
	reqLogger := r.Log.WithValues("Request Namespace", req.Namespace)
	reqLogger.Info("Reconciling Subscription")
	sub := &operatorv1.Subscription{}
	reqLogger.Info("Start finding Subscription")
	err := r.Client.Get(context.TODO(), req.NamespacedName, sub)
	if err != nil {
		reqLogger.Info("Maybe no Subscription in or get error")
		if err := client.IgnoreNotFound(err); err == nil {
			reqLogger.Info("Cannot found any Subscription in", req.NamespacedName)
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, err
		}
	}
	//check whether the subscription file has been operated
	switch sub.Status.OpStatus {
	case "operated":
		//operated do nothing
		reqLogger.Info("Subscription has been operated, not need to operate again!")
		return ctrl.Result{}, nil
	case "not operate":
		//not operate, do create or delete by option
		// List or create BluePrint
		blueprintList := &operatorv1.BluePrintList{}
		err = r.Client.List(context.TODO(), blueprintList)
		if err != nil {
			if err := client.IgnoreNotFound(err); err == nil {
				reqLogger.Info("Cannot found any BluePrint in", sub.Namespace)
				return ctrl.Result{}, nil
			} else {
				return ctrl.Result{}, err
			}
		}
		switch sub.Spec.Option {
		case "delete":
			//check if BluePrint already exists locally
			if len(blueprintList.Items) == 0 {
				reqLogger.Info("No need to delete!")
			} else if len(blueprintList.Items) > 0 {
				for _, blueprint := range blueprintList.Items {
					csvVersion := blueprint.Spec.ClusterServiceVersionNames[0]
					if csvVersion == sub.Spec.StartingCSV {
						err = r.deleteBluePrint(&blueprint)
					}
				}
				reqLogger.Info("All BluePrint of the specified version have been deleted!")
			}
			sub.Status.OpStatus = "operated"
			err = r.Client.Update(context.TODO(), sub)
			if err != nil {
				return ctrl.Result{}, err
			}
			reqLogger.Info("operate subscription successful!")
		case "create":
			//check if BluePrint already exists locally
			if len(blueprintList.Items) != 0 {
				flag := r.CheckSameCategory(blueprintList,sub)
				if !flag {
					_, err := r.ensureBlueprint(sub)
					if err != nil {
						return ctrl.Result{}, err
					}
				} else {
					for _, blueprint := range blueprintList.Items {
						csvVersion := blueprint.Spec.ClusterServiceVersionNames[0]
						if csvVersion == sub.Spec.StartingCSV {
							reqLogger.Info("Same blueprint already installed!")
						} else {
							if r.CompareCategory(csvVersion, sub.Spec.StartingCSV) {
								reqLogger.Info("Delete a same category blueprint!")
								err = r.deleteBluePrint(&blueprint)
								if err != nil {
									return ctrl.Result{}, err
								}
							}
						}
					}
					_, err := r.ensureBlueprint(sub)
					if err != nil {
						return ctrl.Result{}, err
					}
				}
			} else {
				//There is no blueprint in the cluster,create a new one
				_, err := r.ensureBlueprint(sub)
				if err != nil {
					return ctrl.Result{}, err
				}
				reqLogger.Info("Create a correct version BluePrint in the cluster!")
			}
			sub.Status.OpStatus = "operated"
			err = r.Client.Update(context.TODO(), sub)
			if err != nil {
				return ctrl.Result{}, err
			}
			reqLogger.Info("operate subscription successful!")
		}
	}
	return ctrl.Result{}, nil

}

func (r *SubscriptionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorv1.Subscription{}).
		Complete(r)
}

func (r *SubscriptionReconciler) ensureBlueprint(sub *operatorv1.Subscription) (*operatorv1.BluePrint, error) {
	return r.createBluePrint(sub)
}

func (r *SubscriptionReconciler) createBluePrint(sub *operatorv1.Subscription) (*operatorv1.BluePrint, error) {

	blueprint := &operatorv1.BluePrint{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "install-",
			Namespace:    sub.Namespace,
		},
		Spec: operatorv1.BluePrintSpec{
			ClusterServiceVersionNames: []string{sub.Spec.StartingCSV},
		},
		Status: operatorv1.BluePrintStatus{
			Plan: []*operatorv1.Step{
				{
					Resolving: "csv.v.1",
					Resource: operatorv1.StepResource{
						Group:    "operators.coreos.com",
						Version:  "v1",
						Kind:     "CustomResourceDefinition",
						Name:     "csv.v.1",
						Manifest: "{}",
					},
					Status: operatorv1.StepStatusUnknown,
				},
			},
		},
	}
	err := r.Client.Create(context.TODO(), blueprint)
	if err != nil {
		return nil, nil
	}
	return blueprint, nil
}

func (r *SubscriptionReconciler) deleteBluePrint(blueprint *operatorv1.BluePrint) error {

	for i, _ := range blueprint.Status.Plan {
		blueprint.Status.Plan[i].Status = operatorv1.StepStatusDelete
		if err := r.Client.Update(context.TODO(), blueprint); err != nil {
			return err
		}
	}
	return nil
}

func (r *SubscriptionReconciler) CompareCategory(str1 string, str2 string) bool{
	if str1[0:(strings.Index(str1, "."))] == str2[0:(strings.Index(str2, "."))] {
		return true
	}
	return false
}

func (r *SubscriptionReconciler) CheckSameCategory(blueprintList *operatorv1.BluePrintList, sub *operatorv1.Subscription) bool{
	var flag bool = false
	for _, blueprint := range blueprintList.Items {
		csvVersion := blueprint.Spec.ClusterServiceVersionNames[0]
		if r.CompareCategory(csvVersion,sub.Spec.StartingCSV) {
			flag = true
		}
	}
	return flag
}
