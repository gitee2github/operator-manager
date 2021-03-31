package main

import (
	"fmt"
	"gopkg.in/yaml.v2"
	"io/ioutil"
)

func main() {

	resultMap := make(map[string]map[string]interface{})
	yamlFile, err := ioutil.ReadFile("etcdbackups.etcd.database.coreos.com.crd.yaml")
	if err != nil {
		fmt.Println(err.Error())
	}
	err = yaml.Unmarshal(yamlFile, resultMap)
	if err != nil {
		fmt.Println(err.Error())
	}
	fmt.Println(resultMap["kind"])
	fmt.Println(resultMap["metadata"]["name"])
}
