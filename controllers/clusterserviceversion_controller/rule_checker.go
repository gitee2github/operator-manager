package controllers

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/authentication/serviceaccount"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	crbacv1 "k8s.io/client-go/listers/rbac/v1"

	v1alpha1 "github.com/buptGophers/operator-manager/api/v1alpha1"
	rbacauthorizer "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/plugin/pkg/auth/authorizer/rbac"

	ownerutil "github.com/buptGophers/operator-manager/controllers/clusterserviceversion_controller/util/ownerutil"
)

// RuleChecker is used to verify whether PolicyRules are satisfied by existing Roles or ClusterRoles
type RuleChecker interface {
	// RuleSatisfied determines whether a PolicyRule is satisfied for a ServiceAccount
	// by existing Roles and ClusterRoles
	RuleSatisfied(sa *corev1.ServiceAccount, namespace string, rule rbacv1.PolicyRule) (bool, error)
}

// CSVRuleChecker determines whether a PolicyRule is satisfied for a ServiceAccount
// by existing Roles and ClusterRoles
type CSVRuleChecker struct {
	roleLister               crbacv1.RoleLister
	roleBindingLister        crbacv1.RoleBindingLister
	clusterRoleLister        crbacv1.ClusterRoleLister
	clusterRoleBindingLister crbacv1.ClusterRoleBindingLister
	csv                      *v1alpha1.ClusterServiceVersion
}

// NewCSVRuleChecker returns a pointer to a new CSVRuleChecker
func NewCSVRuleChecker(roleLister crbacv1.RoleLister, roleBindingLister crbacv1.RoleBindingLister, clusterRoleLister crbacv1.ClusterRoleLister, clusterRoleBindingLister crbacv1.ClusterRoleBindingLister, csv *v1alpha1.ClusterServiceVersion) *CSVRuleChecker {
	return &CSVRuleChecker{
		roleLister:               roleLister,
		roleBindingLister:        roleBindingLister,
		clusterRoleLister:        clusterRoleLister,
		clusterRoleBindingLister: clusterRoleBindingLister,
		csv:                      csv.DeepCopy(),
	}
}

// RuleSatisfied returns true if a ServiceAccount is authorized to perform all actions described by a PolicyRule in a namespace
func (c *CSVRuleChecker) RuleSatisfied(sa *corev1.ServiceAccount, namespace string, rule rbacv1.PolicyRule) (bool, error) {
	// check if the rule is valid
	err := ruleValid(rule)
	if err != nil {
		return false, fmt.Errorf("rule invalid: %s", err.Error())
	}

	// get attributes set for the given Role and ServiceAccount
	user := toDefaultInfo(sa)
	attributesSet := toAttributesSet(user, namespace, rule)

	// create a new RBACAuthorizer
	rbacAuthorizer := rbacauthorizer.New(c, c, c, c)

	// ensure all attributes are authorized
	for _, attributes := range attributesSet {
		decision, _, err := rbacAuthorizer.Authorize(attributes)
		if err != nil {
			return false, err
		}

		if decision == authorizer.DecisionDeny {
			return false, nil
		}

	}

	return true, nil
}

func (c *CSVRuleChecker) GetRole(namespace, name string) (*rbacv1.Role, error) {
	// get the Role
	role, err := c.roleLister.Roles(namespace).Get(name)
	if err != nil {
		return nil, err
	}

	// check if the Role has an OwnerConflict with the client's CSV
	if role != nil && ownerutil.HasOwnerConflict(c.csv, role.GetOwnerReferences()) {
		return &rbacv1.Role{}, nil
	}

	return role, nil
}

func (c *CSVRuleChecker) ListRoleBindings(namespace string) ([]*rbacv1.RoleBinding, error) {
	// get all RoleBindings
	rbList, err := c.roleBindingLister.RoleBindings(namespace).List(labels.Everything())
	if err != nil {
		return nil, err
	}

	// filter based on OwnerReferences
	var filtered []*rbacv1.RoleBinding
	for _, rb := range rbList {
		if !ownerutil.HasOwnerConflict(c.csv, rb.GetOwnerReferences()) {
			filtered = append(filtered, rb)
		}
	}

	return filtered, nil
}

func (c *CSVRuleChecker) GetClusterRole(name string) (*rbacv1.ClusterRole, error) {
	// get the ClusterRole
	clusterRole, err := c.clusterRoleLister.Get(name)
	if err != nil {
		return nil, err
	}

	// check if the ClusterRole has an OwnerConflict with the client's CSV
	if clusterRole != nil && ownerutil.HasOwnerConflict(c.csv, clusterRole.GetOwnerReferences()) {
		return &rbacv1.ClusterRole{}, nil
	}

	return clusterRole, nil
}

func (c *CSVRuleChecker) ListClusterRoleBindings() ([]*rbacv1.ClusterRoleBinding, error) {
	// get all RoleBindings
	crbList, err := c.clusterRoleBindingLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	// filter based on OwnerReferences
	var filtered []*rbacv1.ClusterRoleBinding
	for _, crb := range crbList {
		if !ownerutil.HasOwnerConflict(c.csv, crb.GetOwnerReferences()) {
			filtered = append(filtered, crb)
		}
	}

	return filtered, nil
}

// ruleValid returns an error if the given PolicyRule is not valid (resource and nonresource attributes defined)
func ruleValid(rule rbacv1.PolicyRule) error {
	if len(rule.Verbs) == 0 {
		return fmt.Errorf("policy rule must have at least one verb")
	}

	resourceCount := len(rule.APIGroups) + len(rule.Resources) + len(rule.ResourceNames)
	if resourceCount > 0 && len(rule.NonResourceURLs) > 0 {
		return fmt.Errorf("rule cannot apply to both regular resources and non-resource URLs")
	}

	return nil
}

func toDefaultInfo(sa *corev1.ServiceAccount) *user.DefaultInfo {
	// TODO(Nick): add Group if necessary
	return &user.DefaultInfo{
		Name: serviceaccount.MakeUsername(sa.GetNamespace(), sa.GetName()),
		UID:  string(sa.GetUID()),
	}
}

// toAttributesSet converts the given user, namespace, and PolicyRule into a set of Attributes expected. This is useful for checking
// if a composed set of Roles/RoleBindings satisfies a PolicyRule.
func toAttributesSet(user user.Info, namespace string, rule rbacv1.PolicyRule) []authorizer.Attributes {
	set := map[authorizer.AttributesRecord]struct{}{}

	// add empty string for empty groups, resources, resource names, and non resource urls
	groups := rule.APIGroups
	if len(groups) == 0 {
		groups = make([]string, 1)
	}
	resources := rule.Resources
	if len(resources) == 0 {
		resources = make([]string, 1)
	}
	names := rule.ResourceNames
	if len(names) == 0 {
		names = make([]string, 1)
	}
	nonResourceURLs := rule.NonResourceURLs
	if len(nonResourceURLs) == 0 {
		nonResourceURLs = make([]string, 1)
	}

	for _, verb := range rule.Verbs {
		for _, group := range groups {
			for _, resource := range resources {
				for _, name := range names {
					for _, nonResourceURL := range nonResourceURLs {
						set[attributesRecord(user, namespace, verb, group, resource, name, nonResourceURL)] = struct{}{}
					}
				}
			}
		}
	}

	attributes := make([]authorizer.Attributes, len(set))
	i := 0
	for attribute := range set {
		attributes[i] = attribute
		i++
	}
	log.Debugf("attributes set %+v", attributes)

	return attributes
}

// attribute creates a new AttributesRecord with the given info. Currently RBAC authz only looks at user, verb, apiGroup, resource, and name.
func attributesRecord(user user.Info, namespace, verb, apiGroup, resource, name, path string) authorizer.AttributesRecord {
	resourceRequest := path == ""
	return authorizer.AttributesRecord{
		User:            user,
		Verb:            verb,
		Namespace:       namespace,
		APIGroup:        apiGroup,
		Resource:        resource,
		Name:            name,
		ResourceRequest: resourceRequest,
		Path:            path,
	}
}
