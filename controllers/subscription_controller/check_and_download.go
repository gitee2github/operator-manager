package controllers

import (
	"encoding/json"
	"errors"
	log "github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const (
	operatorHubAPI = "https://operatorhub.io/api/operator"
	communityUrl   = "http://localhost:8019"
)

func checkAndDownload(operator, version string) (bool, error) {
	valid, err := validate(operator, version)
	if err != nil {
		return false, err
	}
	if valid {
		res, err := http.Get(communityUrl)
		if err != nil {
			return false, err
		}
		defer res.Body.Close()
		if res.StatusCode == 200 {
			log.Info("Available version detected! Start the installation process!")
			err = getResource(operator, version)
			if err != nil {
				return false, err
			}
		}
		return true, nil
	}
	return false, errors.New("invalid")
}

func validate(operator, version string) (bool, error) {
	packageResponse, err := http.Get(operatorHubAPI + "?packageName=" + operator)
	if err != nil {
		return false, err
	}
	defer packageResponse.Body.Close()
	body, err := ioutil.ReadAll(packageResponse.Body)
	if err != nil {
		return false, err
	}
	resultMap := make(map[string]map[string]interface{})
	if json.Unmarshal(body, &resultMap) != nil {
		return false, err
	}
	op := resultMap["operator"]["channels"]
	return strings.Contains(strval(op), version), nil
}

func getResource(operator, version string) error {
	response, err := http.Get(communityUrl + "/raw?operator=" + operator + "&version=" + version)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == 201 {
		return errors.New("unable to find operator subscribed")
	}
	// Download the subscribed operator into ./config/bundles
	if response.StatusCode == 202 {
		body, err := ioutil.ReadAll(response.Body)
		if err != nil {
			return err
		}

		resultMap := make(map[string][]string)
		err = json.Unmarshal(body, &resultMap)
		if err != nil {
			return err
		}
		for _, filepath := range resultMap["filepath"] {
			if downloadFile(operator, version, filepath) != nil {
				return err
			}
		}
	}
	return nil
}

func downloadFile(operator, version, filrname string) error {
	r, err := http.Get(communityUrl + "/getFile?operator=" + operator + "&version=" + version + "&filename=" + filrname)
	if err != nil {
		return err
	}
	defer func() { _ = r.Body.Close() }()

	err = os.MkdirAll("./config/bundles/"+operator+"/"+version, 0766)
	if err != nil {
		return err
	}

	f, err := os.Create("./config/bundles/" + operator + "/" + version + "/" + filrname)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = io.Copy(f, r.Body)
	if err != nil {
		return err
	}
	return nil
}

// interface to string
func strval(value interface{}) string {
	var key string
	if value == nil {
		return key
	}

	switch value.(type) {
	case float64:
		ft := value.(float64)
		key = strconv.FormatFloat(ft, 'f', -1, 64)
	case float32:
		ft := value.(float32)
		key = strconv.FormatFloat(float64(ft), 'f', -1, 64)
	case int:
		it := value.(int)
		key = strconv.Itoa(it)
	case uint:
		it := value.(uint)
		key = strconv.Itoa(int(it))
	case int8:
		it := value.(int8)
		key = strconv.Itoa(int(it))
	case uint8:
		it := value.(uint8)
		key = strconv.Itoa(int(it))
	case int16:
		it := value.(int16)
		key = strconv.Itoa(int(it))
	case uint16:
		it := value.(uint16)
		key = strconv.Itoa(int(it))
	case int32:
		it := value.(int32)
		key = strconv.Itoa(int(it))
	case uint32:
		it := value.(uint32)
		key = strconv.Itoa(int(it))
	case int64:
		it := value.(int64)
		key = strconv.FormatInt(it, 10)
	case uint64:
		it := value.(uint64)
		key = strconv.FormatUint(it, 10)
	case string:
		key = value.(string)
	case []byte:
		key = string(value.([]byte))
	default:
		newValue, _ := json.Marshal(value)
		key = string(newValue)
	}
	return key
}
