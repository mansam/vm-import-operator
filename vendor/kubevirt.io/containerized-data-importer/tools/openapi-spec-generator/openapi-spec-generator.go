/*
Copyright 2018 The CDI Authors.

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

package main

import (
	"encoding/json"
	"fmt"

	"github.com/emicklei/go-restful"

	"kubevirt.io/containerized-data-importer/pkg/apiserver"
)

func dumpOpenAPISpec(apiws []*restful.WebService) {
	openapispec := loadOpenAPISpec(apiws)
	data, err := json.MarshalIndent(openapispec, " ", " ")
	if err != nil {
		fmt.Println(err)
		panic(err)
	}

	fmt.Println(string(data))
}

func main() {
	webservices := apiserver.UploadTokenRequestAPI()
	webservices = append(webservices, CoreAPI()...)
	dumpOpenAPISpec(webservices)
}