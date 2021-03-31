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
	reqLogger := r.Log.WithValues("Request Namespace", req.Namespace)
	reqLogger.Info("Reconciling Subscription")
	sub := &operatorv1.Subscription{}
	err := r.Client.Get(context.TODO(), req.NamespacedName, sub)
	if err != nil {
		if err := client.IgnoreNotFound(err); err == nil {
			reqLogger.Info("Cannot find any Subscription")
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, err
		}
	}

	switch sub.Status.OpStatus {
	case "present":
		return ctrl.Result{}, nil
	case "unknown":
		//unknown, create or delete through sub.Spec.Option
		blueprintList := &operatorv1.BluePrintList{}
		err = r.Client.List(context.TODO(), blueprintList)
		if err != nil {
			if err := client.IgnoreNotFound(err); err == nil {
				reqLogger.Info("Cannot find any BluePrint locally")
			} else {
				return ctrl.Result{}, err
			}
		}

		switch sub.Spec.Option {
		case "delete":
			//check if operator has already existed locally
			if len(blueprintList.Items) == 0 {
				reqLogger.Info("No need to delete!")
			} else if len(blueprintList.Items) > 0 {
				for _, blueprint := range blueprintList.Items {
					csvVersion := blueprint.Spec.ClusterServiceVersion
					if csvVersion == sub.Spec.StartingCSV {
						err = r.deleteBluePrint(&blueprint)
					}
				}
				reqLogger.Info("All BluePrint of the specified version have been deleted!")
			}
			sub.Status.OpStatus = "present"
		case "create":
			b, err := r.ensureBlueprint(reqLogger, sub, blueprintList)
			if err != nil {
				return ctrl.Result{}, err
			}
			if b == nil {
				reqLogger.Info("Cannot find because of invalid subscription!")
				return ctrl.Result{}, nil
			}
			sub.Status.OpStatus = "present"
		}
		err = r.Client.Update(context.TODO(), sub)
		if err != nil {
			return ctrl.Result{}, err
		}
		reqLogger.Info("operate subscription successful!")
	}
	return ctrl.Result{}, nil
}

func (r *SubscriptionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorv1.Subscription{}).
		Complete(r)
}

func (r *SubscriptionReconciler) ensureBlueprint(reqLogger logr.Logger, sub *operatorv1.Subscription, blueprintList *operatorv1.BluePrintList) (*operatorv1.BluePrint, error) {
	// Check from operatorhub.io and download the CSV&CRDs resources if valid
	valid, err := checkAndDownload(SplitStartingCSV(sub.Spec.StartingCSV))
	if !valid {
		return nil, nil
	}

	if err != nil {
		if err := client.IgnoreNotFound(err); err != nil {
			return r.createBluePrint(reqLogger, sub, "NULL", operatorv1.StepStatusUnknown)
		}
		reqLogger.Info("Fail to check or download subscribed operator!")
		sub.Status.OpStatus = "present"
		err = r.Client.Update(context.TODO(), sub)
		return nil, err
	}

	blueprint, existed, matched := r.CheckBlueprint(sub, blueprintList)
	if !existed {
		blueprint2, err := r.createBluePrint(reqLogger, sub, "NULL", operatorv1.StepStatusUnknown)
		if err != nil {
			reqLogger.Error(err, err.Error())
		}
		return blueprint2, err
	}

	if !matched {
		blueprint1, err := r.createBluePrint(reqLogger, sub, blueprint.Spec.ClusterServiceVersion, operatorv1.StepStatusPresent)
		if err != nil {
			reqLogger.Error(err, err.Error())
		}
		return blueprint1, err
	}
	// No need to create blueprint because the subscribed blueprint already existed
	return blueprint, nil
}

func (r *SubscriptionReconciler) createBluePrint(reqLogger logr.Logger, sub *operatorv1.Subscription, present string, exeStatus operatorv1.StepStatus) (*operatorv1.BluePrint, error) {
	blueprint := &operatorv1.BluePrint{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "blueprint-",
			Namespace:    sub.Namespace,
		},
		Spec: operatorv1.BluePrintSpec{
			ClusterServiceVersion: sub.Spec.StartingCSV,
			OldVersion:            present,
		},
		Status: operatorv1.BluePrintStatus{
			Plan: operatorv1.Step{
				Resolving: "csv.v.1",
				Resource: operatorv1.StepResource{
					Group:    "operators.coreos.com",
					Version:  "v1",
					Kind:     "CustomResourceDefinition",
					Name:     "csv.v.1",
					Manifest: "{}",
				},
				Status: exeStatus,
			},
		},
	}
	err := r.Client.Create(context.TODO(), blueprint)
	if err != nil {
		reqLogger.Info("Create error!")
		return nil, err
	}
	return blueprint, nil
}

func (r *SubscriptionReconciler) deleteBluePrint(blueprint *operatorv1.BluePrint) error {
	blueprint.Status.Plan.Status = operatorv1.StepStatusDelete
	if err := r.Client.Update(context.TODO(), blueprint); err != nil {
		return err
	}
	return nil
}

func (r *SubscriptionReconciler) CheckBlueprint(sub *operatorv1.Subscription, blueprintList *operatorv1.BluePrintList) (*operatorv1.BluePrint, bool, bool) {
	for _, blueprint := range blueprintList.Items {
		if blueprint.Spec.ClusterServiceVersion == sub.Spec.StartingCSV {
			return &blueprint, true, true
		}
		if r.checkVersion(blueprint.Spec.ClusterServiceVersion, sub.Spec.StartingCSV) {
			return &blueprint, true, false
		}
	}
	return nil, false, false
}

func (r *SubscriptionReconciler) checkVersion(str1 string, str2 string) bool {
	return str1[:4] == str2[:4] && str1[(strings.Index(str1, "."))+1:] != str2[(strings.Index(str2, "."))+1:]
}

func SplitStartingCSV(StartingCSV string) (string, string) {
	return StartingCSV[:strings.IndexAny(StartingCSV, ".")], StartingCSV[strings.IndexAny(StartingCSV, ".")+1:]
}
