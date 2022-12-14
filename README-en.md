# operator-manager
[![](https://img.shields.io/badge/language-Go-brightgreen.svg)](https://github.com/golang/go)
[![](https://img.shields.io/badge/Dependence-Kubebuilder-blue.svg)](https://book.kubebuilder.io/) 
[![](https://img.shields.io/badge/Reference-OLM-important.svg)](https://operatorframework.io)

## Overview

`operator-manager` is a lightweight `operator` management framework based on the [kubebuilder](https://book.kubebuilder.io/) architecture. Based on customized [Kubernetes](https://kubernetes.io/) resources, the framework deploys `operator` of the required version through stateful operations management and supports `operator` uninstallation and version update. In this way, cloud-native applications can be easily managed.

## Overall Architecture

<img src="https://z3.ax1x.com/2021/03/31/cA4nw6.png" alt="Architecture" style="zoom:30%;" align="left"/>

`operator-manager` is designed based on the principle of using `operator` to manage `operator`. `operator` is developed based on Kubernetes, which helps O&M personnel efficiently manage stateful applications. `operator-manager` considers the `operator` to be managed as a type of stateful application and manages the `crd`, `service account`, and `webhook` dependencies of the `operator`.

`operator-manager` is based on the following three types of `crd` and `controller`.

| Resource              | Controller| Version                            | Description                                                  |
| --------------------- | -------------------------------- | --------------------------------| ------------------------------------------------------------ |
| ClusterServiceVersion | ClusterServiceVersion_Controller | v1alpha1                        | Includes the application metadata, name, version, label, required resources, and installation policy of `operator`. |
| BluePrint             | BluePrint_Controller             | v1                              | Parses and deploys `crd` resources on which `csv` and `operator` depend, and implements version management based on `BluePrint` in the cluster.|
| Subscription          | Subscription_Controller          | v1                              | Allows users to initiate subscription requests by modifying subscription-related instances deployed in the cluster.|

| Controller                       | Creatable Resource        |
| -------------------------------- | -------------------------- |
| ClusterServiceVersion_Controller | Deployment                 |
| ClusterServiceVersion_Controller | Service Account            |
| ClusterServiceVersion_Controller | (Cluster) Roles             |
| ClusterServiceVersion_Controller | (Cluster) RoleBindings      |
| BluePrint_Controller             | Custom Resource Definition |
| BluePrint_Controller             | ClusterServiceVersion      |
| Subscription_Controller          | BluePrint                  |

## Design

Compared with the [OLM](https://operatorframework.io) architecture designed by `CoreOS`, the lightweight `operator` management architecture does not support directory management, which reduces resource overheads. `operator-manager` consists of `Subscription_Controller`, `BluePrint_Controller`, and `ClusterServiceVersion_Controller`. `Subscription_Controller` processes subscription requests initiated by users, interacts with [OperatorHub](https://operatorhub.io/), completes operator verification, downloads required dependency resources from the cloud server, and parses user subscriptions to create the corresponding `BluePrint CRD`. `BluePrint_Controller` parses dependencies and creates CRDs for the operators to be created, upgraded, rolled back, or deleted. `ClusterServiceVersion_Controller` deploys the operators to be created, upgraded, rolled back, or deleted to the cluster through the `deployment` controller. After checking the dependent resources and permissions, `ClusterServiceVersion_Controller` deploys `service account` and `RBAC rules`.

## Function Overview

| Function                | Completion Status           |
| ------------------- | ------------------ |
| Initiating subscription requests    | :heavy_check_mark: |
| Resolving operator dependencies | :heavy_check_mark: |
| Installing and deploying operators   | :heavy_check_mark: |
| Managing versions            | :heavy_check_mark: |
| Binding service accounts| :heavy_check_mark: |
| Managing permissions            | :heavy_check_mark: |
| Checking states  | :heavy_check_mark: |
| Uninstalling and deleting operators   | :heavy_check_mark: |
| Interconnecting with operatorhub.io| :heavy_check_mark: |
| Web Dashboard       | :x:                |


## Prerequisites
- [git][git_tool]
- [go][go_tool] version v1.13+
- [docker][docker_tool] version 17.03+
- [kubectl][kubectl_tool] version v1.11.3+
- [kubebuilder][kubebuilder_tool] 
- Access to a Kubernetes v1.16.3+ cluster

`Makefile` exists in the root directory of the project. After this file is executed, the following tools are installed during project creation:

- [kustomize](https://kubernetes-sigs.github.io/kustomize/) 
- [controller-gen](https://github.com/kubernetes-sigs/controller-tools) 


## Preparations

### Installation

1. Execute the `cd $GOPATH/src/github.com && mkdir -p buptGophers && cd buptGophers` command.
2. Download the project source code to the local repository and execute the `git clone https://gitee.com/openeuler2020/team-1924513571.git` command.
3. Change the project name, ensure that the project path is correct, and execute the `mv team-1924513571 operator-manager && cd operator-manager` command.
4. Execute the `make install` command to install the resources required by the `operator-manager` project based on `Makefile`.

**Note:** This project supports deployment based on the ARM/AMD architecture. After the `make install` command is executed, an installation error may occur. Ensure that the following operation is performed: `ENV GOPROXY="https://goproxy.io`/`ENV GO111MODULE=onGOPROXY`. If you encounter other problems, please contact us.

## Local Use

1. Start the project.

    Execute the `make run` command on the local host.
    
2. Initiate a subscription request.

   After the project is started, manually deploy the `cr` instance of `subscription` to the cluster to initiate the `operator` management.
   For details about the `cr` instance, see `/config/samples` in the project directory.

   ```yaml
   apiVersion: operators.coreos.com.operator-manager.domain/v1
   kind: Subscription
   metadata:
     name: subscription-sample
   spec:
     # Add fields here
     startingCSV: prometheus.0.22.2
     option: create
     foo: bar
   status:
     opstatus: not operate
   ```
   **NOTE:** If you want to delete it, change the `spec.option` field to `delete`.

3. Monitor the cluster running status.
   
    View the running status of `operator-manager` based on the log information printed by the cluster. Alternatively, execute the `kubect` command to obtain the running status of resources such as `subscription`, `blueprint`, `clusterserviceversion`, `crd`, `deployment`, `pod`, `serviceaccount`, and `RBAC rules` to check whether the user subscriptions are complete.

4. Perform other operations.

    Directly design the `cr` instance of `blueprint` to complete the `operator` management operation. However, much information needs to be manually modified for the `cr` instance of `blueprint`. It is recommended that the `cr` instance of `subscription` perform the `operator` management operation.

5. Start the file server.

    Access the `community-operators` directory and execute the `go mod download` command to configure the `Gin` environment. Execute the `go run main.go` command to start the local file server.

# Key Concepts

## Subscription

Allows users to initiate subscription requests by modifying subscription-related instances deployed in the cluster.

## BluePrint

Parses and deploys `crd` resources on which `csv` and `operator` depend, and implements version management based on `blueprint` in the cluster.

## ClusterServiceVersion

It includes:

- Application metadata (name, description, version definition, link, icon, and label)
- Installation policy, including the deployment set and permission set (such as service accounts, RBAC roles and binding) required during the installation of `operator`.
- CRDs, including the type of a `CRD`, service to which a `CRD` belongs, Kubernetes native resources that a `CRD` interacts with, and `fields` descriptors that contain model semantic information, such as `operator`, `spec`, and `status`.

## CRD

To reduce the cost of running applications in `Kubernetes`, OLM can define other metadata on applications to discover and resolve dependencies between running applications.
Each `operator` must define the following content:

- Managed `CRD`. For example, `Operator Etcd` manages `EtcdCluster`.
- Dependent `CRD`. For example, `Operator Vault` depends on `EtcdCluster` because Vault is supported by etcd.
  Then, by looking for the appropriate `operator` to manage and install the `CRD` required by the application, the basic dependencies are resolved. The way in which users interact with directories may further limit dependency resolution.

### Controller

Controller is the core concept of Kubernetes. The controller ensures that the actual state (including the cluster state and external state) matches the expected state of any given object. This process is called reconciliation. In `controller-runtime`, the logic for implementing reconciliation for a specific class is called `reconciler`, which is the key logic of the controller.

## Acknowledgment

- [OpenEuler](https://www.openeuler.org/en/): `operator-manager` uses the OpenEuler operating system for project development.
- [OSCHINA](https://www.oschina.net/): `operator-manager` project originates from the OSCHINA open source competition.
- [Pengcheng Laboratory](http://www.szpclab.com/): `operator-manager` project is developed and tested on the Kunpeng servers of the Pengcheng Laboratory.

[architecture]: /doc/design/architecture.md
[philosophy]: /doc/design/philosophy.md
[installation guide]: /doc/install/install.md
[git_tool]:https://git-scm.com/downloads
[go_tool]:https://golang.org/dl/
[docker_tool]:https://docs.docker.com/install/
[kubectl_tool]:https://kubernetes.io/docs/tasks/tools/install-kubectl/
[kubebuilder_tool]:https://book.kubebuilder.io/quick-start.html
