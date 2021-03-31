package controllers

const (
	crdKind                = "CustomResourceDefinition"
	secretKind             = "Secret"
	clusterRoleKind        = "ClusterRole"
	clusterRoleBindingKind = "ClusterRoleBinding"
	configMapKind          = "ConfigMap"
	csvKind                = "ClusterServiceVersion"
	serviceAccountKind     = "ServiceAccount"
	serviceKind            = "Service"
	roleKind               = "Role"
	roleBindingKind        = "RoleBinding"
	generatedByKey         = "olm.generated-by"
	maxInstallPlanCount    = 5
	maxDeletesPerSweep     = 5
	RegistryFieldManager   = "olm.registry"
)
