# Operator-manager
[![](https://img.shields.io/badge/language-Go-brightgreen.svg)](https://github.com/golang/go)
[![](https://img.shields.io/badge/Dependence-Kubebuilder-blue.svg)](https://book.kubebuilder.io/) 
[![](https://img.shields.io/badge/Reference-OLM-important.svg)](https://operatorframework.io)

## 概览

`Operator-Manager`是一套基于[kubebuilder](https://book.kubebuilder.io/) 架构设计的轻量化`Operator`管理框架，该框架主要在[kubenetes](https://kubernetes.io/) 自定义资源基础之上，通过有状态运营管理模式部署所需版本的`Operator`，并可支持`Operator`卸载、版本更新等操作，以实现便捷的云原生应用管理。

## 整体架构

<img src="https://z3.ax1x.com/2021/03/31/cA4nw6.png" alt="Architecture" style="zoom:30%;" align="left"/>

`Operator-Manager`的设计原则是使用`operator`管理`operator`，`operator`基于Kubernetes二次开发，可以帮助运维人员高效管理有状态应用。`operator-manager`将要管理的`operator`视为一类有状态应用，管理`operator`的`crd`、`service account`、`webhook`等依赖。

`Operator-Manager`下主要基于以下三类`crd`和`controller`。

| Resource              | 所属controller                    | 版本                            | Description                                                  |
| --------------------- | -------------------------------- | --------------------------------| ------------------------------------------------------------ |
| ClusterServiceVersion | ClusterServiceVersion_Controller | v1alpha1                        | 包括Operator的应用元数据、名称、版本、标志、所需资源、安装策略等... |
| BluePrint             | BluePrint_Controller             | v1                              | 解析并部署csv和operator依赖的crd资源，并可基于集群内部的BluePrint实现版本管理 |
| Subscription          | Subscription_controller          | v1                              | 与用户对接，用户通过修改部署在集群中的subscription的相关实例发起请求 |

| controller                       | Creatable Resources        |
| -------------------------------- | -------------------------- |
| ClusterServiceVersion_Controller | Deployment                 |
| ClusterServiceVersion_Controller | Service Account            |
| ClusterServiceVersion_Controller | (Cluster)Roles             |
| ClusterServiceVersion_Controller | (Cluster)RoleBindings      |
| BluePrint_Controller             | Custom Resource Definition |
| BluePrint_Controller             | ClusterServiceVersion      |
| Subscription_Controller          | BluePrint                  |

## 设计

相比于`CoreOS`设计的[OLM](https://operatorframework.io)架构，我们设计的轻量级`operator`管理架构取消了目录管理，降低资源开销。`Operator Manager`由`Subscription Controller`、`BluePrint Controller`和`ClusterServiceVersion Controller`三个Controller组成。`Subscription Controller`负责处理用户发起的订阅请求，与[operatorhub](https://operatorhub.io/)交互，完成operator校验，并从云服务器下载所需依赖资源，解析用户订阅创建对应的`BluePrint CRD`。`BluePrint Controller`负责进行依赖解析，创建待创建/升级/回滚/删除的operator的CRD。`ClusterServiceVersion Controller`负责将待创建/升级/回滚/删除的operator通过`deployment`控制器部署到集群中去，完成依赖资源、依赖权限检查后，部署`service account`, `RBAC rules`。

## 功能概览

| 功能                | 完成情况           |
| ------------------- | ------------------ |
| 用户发起订阅请求    | :heavy_check_mark: |
| operator 依赖解析   | :heavy_check_mark: |
| 安装部署 operator   | :heavy_check_mark: |
| 版本管理            | :heavy_check_mark: |
| 绑定service account | :heavy_check_mark: |
| 权限管理            | :heavy_check_mark: |
| 状态检查            | :heavy_check_mark: |
| 卸载删除 operator   | :heavy_check_mark: |
| 对接 operatorhub.io | :heavy_check_mark: |
| Web Dashboard       | :x:                |


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
   
    用户可根据集群打印的日志信息查看`operator-manager`的运行状态，也可以通过`kubect`指令获取`subscription`,`blueprint`,`clusterserviceversion`,`crd`,`deployment`,`pod`,`serviceaccount`, `RBAC rules`等相关资源的运行状态，以检查用户订阅内容是否完成。

4. 其他操作

    用户也可直接设计`BluePrint`的`cr`实例，完成`operator`的管理操作。但`BluePrint`的`cr`实例所需手动修改信息较多，建议用户由`Subscription`的`cr`完成`operator`管理操作。

5. 启动文件服务器

    用户可接入`community-operators`目录下，执行`go mod download`操作配置`Gin`环境。执行`go run main.go`即可启动本地文件服务器。    

# 关键概念

## Subscription

与用户对接，用户通过修改部署在集群中的subscription的相关实例发起请求

## BluePrint

解析并部署`csv`和`operator`依赖的`crd`资源，并可基于集群内部的`BluePrint`实现版本管理

## ClusterServiceVersion

包含：

- 应用元数据（名称，描述，版本定义，链接，图标，标签等）；
- 安装策略，包括 `Operator` 安装过程中所需的部署集合和 `service accounts`，RBAC 角色和绑定等权限集合
- CRDs：包括 `CRD` 的类型，所属服务，`Operator `交互的其他 K8s 原生资源和 `spec`，`status` 这些包含了模型语义信息的` fields` 字段描述符等。

## CRD

为了简化在`Kubernetes`运行应用程序的成本，OLM可以通过在应用程序上定义其他元数据，发现运行的应用程序之间的依赖关系并进行解析。
每一个`Operator`必须定义以下内容：

- 它负责管理的`CRD`，例如`Operator Etcd`用于管理`EtcdCluster`；
- 它依赖的`CRD`，例如`Operator Valut`依赖于`EtcdCluster`，因为Valut是由etcd所支持。
  然后，通过寻找相应的`Operator`来管理和安装应用程序所需的`CRD`，进而实现基本的依赖项解析。用户与目录进行交互的方式可能会进一步限制依赖性解析

### Controller

Controller是 `Kubernetes` 的核心概念。控制器的工作是确保实际状态（包括集群状态，以及外部状态）与任何给定的对象的期望状态相匹配,这个过程称为 reconciling。在 `controller-runtime` 中，为特定种类实现 reconciling 的逻辑称为 `Reconciler`, 该逻辑是Controller的关键逻辑。

## 致谢

- [OpenEuler](https://openeuler.org/): `Operator-manager` 采用 OpenEuler操作系统进行项目开发；
- [OSCHINA](https://www.oschina.net/): `Operator-manager`项目来源于OSCHINA开源比赛；
- [鹏城实验室](https://www.pcl.ac.cn/): `Operator-manager` 项目在鹏城实验室的鲲鹏服务器上进行开发测试；

[architecture]: /doc/design/architecture.md
[philosophy]: /doc/design/philosophy.md
[installation guide]: /doc/install/install.md
[git_tool]:https://git-scm.com/downloads
[go_tool]:https://golang.org/dl/
[docker_tool]:https://docs.docker.com/install/
[kubectl_tool]:https://kubernetes.io/docs/tasks/tools/install-kubectl/
[kubebuilder_tool]:https://book.kubebuilder.io/quick-start.html