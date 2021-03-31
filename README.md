# Operator-manager
<a href="https://github.com/golang/go">
        <img src="https://img.shields.io/badge/language-Go-green.svg">
    </a> 
<a href="https://book.kubebuilder.io/">
        <img src="https://img.shields.io/badge/Dependence-Kubebuilder-blue.svg">
    </a> 
<a href="https://operatorframework.io/">
        <img src="https://img.shields.io/badge/Reference-OLM-brightgreen.svg">
    </a> 

## 概览

`Operator-Manager`是一套基于[kubebuilder](https://book.kubebuilder.io/) 架构设计的轻量化`Operator`管理框架，该框架主要在[kubenetes](https://kubernetes.io/) 自定义资源基础之上，通过有状态运营管理模式部署所需版本的Operator，并可支持`Operator`卸载、版本更新等操作，以实现便捷的云原生应用管理。如需详细了解`operator-manager`，请参考[详细设计]() 。

## 功能概览

#### 快速发起订阅请求
完成安装与启动过程后，用户只需编辑并部署订阅信息，集群中运行的`controllers`将会依据订阅内容及时完成处理，并在完成后更新订阅状态。

### operator依赖解析
With OLMs packaging format Operators can express dependencies on the platform and on other Operators. They can rely on OLM to respect these requirements as long as the cluster is up. In this way, OLMs dependency model ensures Operators stay working during their long lifecycle across multiple updates of the platform or other Operators.

### Discoverability
OLM advertises installed Operators and their services into the namespaces of tenants. They can discover which managed services are available and which Operator provides them. Administrators can rely on catalog content projected into a cluster, enabling discovery of Operators available to install.

### Cluster Stability
Operators must claim ownership of their APIs. OLM will prevent conflicting Operators owning the same APIs being installed, ensuring cluster stability.

### Declarative UI controls
Operators can behave like managed service providers. Their user interface on the command line are APIs. For graphical consoles OLM annotates those APIs with descriptors that drive the creation of rich interfaces and forms for users to interact with the Operator in a natural, cloud-like way.

## Prerequisites
- [git][git_tool]
- [go][go_tool] version v1.13+.
- [docker][docker_tool] version 17.03+.
- [kubectl][kubectl_tool] version v1.11.3+.
- [kubebuilder][kubebuilder_tool] 
- Access to a Kubernetes v1.16.3+ cluster.

项目根目录存在`MakeFile`文件，执行该文件后会在创建项目的过程中安装以下工具：
- [kustomize](https://kubernetes-sigs.github.io/kustomize/) 
- [controller-gen](https://github.com/kubernetes-sigs/controller-tools) 


## 如何开始

### 安装

1. 执行`cd $GOPATH/src/github.com && mkdir -p buptGophers && cd buptGophers`
2. 下载项目源码至本地仓库，执行`git clone https://gitee.com/openeuler2020/team-1924513571.git`
3. 修改项目名称，确保项目路径正确，执行`mv team-1924513571 operator-manager && cd operator-manager`
4. 依据`MAKEFILE`安装`operator-manager`项目所需依赖资源，执行`make install`

**NOTE:** 该项目现支持ARM/AMD架构部署，在执行`make install`指令后可能会遇到安装错误，请确保如下操作：`ENV GOPROXY="https://goproxy.io`/`ENV GO111MODULE=onGOPROXY`，如遇到其他问题请与我们联系.

## 在本地使用

1. 启动项目

    在本地执行`make run`
    
2. 订阅请求

    项目正常启动后，用户即可通过部署`subscription`的`cr`实例，并将该`cr`实例手动部署到集群，以发起`operator`的管理工作。
    `cr`实例请参见项目目录`/config/samples`.
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
   **NOTE:** 如需删除请将`spec.option`字段修改为`delete`。

3. 监测集群运行状态
    
    用户可根据集群打印的日志信息查看`operator-manager`的运行状态，也可以通过`kubect`指令获取`subscription`,`blueprint`,`clusterserviceversion`,`crd`,`deployment`,`pod`,`serviceaccount`等相关资源的运行状态，以检查用户订阅内容是否完成。

4. 其他操作

    用户也可直接设计`BluePrint`的`cr`实例，完成`operator`的管理操作。但`BluePrint`的`cr`实例所需手动修改信息较多，建议用户由`Subscription`的`cr`完成`operator`管理操作。

# Key Concepts

## Subscription

OLM standardizes interactions with operators by requiring that the interface to an operator be via the Kubernetes API. Because we expect users to define the interfaces to their applications, OLM currently uses CRDs to define the Kubernetes API interactions.  

Examples: [EtcdCluster CRD](https://github.com/operator-framework/community-operators/blob/master/community-operators/etcd/0.9.4/etcdclusters.etcd.database.coreos.com.crd.yaml), [EtcdBackup CRD](https://github.com/operator-framework/community-operators/blob/master/community-operators/etcd/0.9.4/etcdbackups.etcd.database.coreos.com.crd.yaml)

## BluePrint

OLM introduces the notion of “descriptors” of both `spec` and `status` fields in kubernetes API responses. Descriptors are intended to indicate various properties of a field in order to make decisions about their content. For example, this can drive connecting two operators together (e.g. connecting the connection string from a mysql instance to a consuming application) and be used to drive rich interactions in a UI.

[See an example of a ClusterServiceVersion with descriptors](https://github.com/operator-framework/community-operators/blob/master/community-operators/etcd/0.9.2/etcdoperator.v0.9.2.clusterserviceversion.yaml)

## ClusterServiceVersion

To minimize the effort required to run an application on kubernetes, OLM handles dependency discovery and resolution of applications running on OLM.

This is achieved through additional metadata on the application definition. Each operator must define:

 - The CRDs that it is responsible for managing. 
   - e.g., the etcd operator manages `EtcdCluster`.
 - The CRDs that it depends on. 
   - e.g., the vault operator depends on `EtcdCluster`, because Vault is backed by etcd.

Basic dependency resolution is then possible by finding, for each “required” CRD, the corresponding operator that manages it and installing it as well. Dependency resolution can be further constrained by the way a user interacts with catalogs.

## ClusterServiceVersion

To minimize the effort required to run an application on kubernetes, OLM handles dependency discovery and resolution of applications running on OLM.

This is achieved through additional metadata on the application definition. Each operator must define:

 - The CRDs that it is responsible for managing. 
   - e.g., the etcd operator manages `EtcdCluster`.
 - The CRDs that it depends on. 
   - e.g., the vault operator depends on `EtcdCluster`, because Vault is backed by etcd.

Basic dependency resolution is then possible by finding, for each “required” CRD, the corresponding operator that manages it and installing it as well. Dependency resolution can be further constrained by the way a user interacts with catalogs.

### Controllers

Dependency resolution is driven through the `(Group, Version, Kind)` of CRDs. This means that no updates can occur to a given CRD (of a particular Group, Version, Kind) unless they are completely backward compatible.

There is no way to express a dependency on a particular version of an operator (e.g. `etcd-operator v0.9.0`) or application instance (e.g. `etcd v3.2.1`). This encourages application authors to depend on the interface and not the implementation.





[architecture]: /doc/design/architecture.md
[philosophy]: /doc/design/philosophy.md
[installation guide]: /doc/install/install.md
[git_tool]:https://git-scm.com/downloads
[go_tool]:https://golang.org/dl/
[docker_tool]:https://docs.docker.com/install/
[kubectl_tool]:https://kubernetes.io/docs/tasks/tools/install-kubectl/
[kubebuilder_tool]:https://book.kubebuilder.io/quick-start.html

[![Kubebuilder](https://book.kubebuilder.io/logos/logo-single-line.png)](https://book.kubebuilder.io/)  [![Operatorframework](https://operatorframework.io/images/logo.svg)](https://operatorframework.io/) 