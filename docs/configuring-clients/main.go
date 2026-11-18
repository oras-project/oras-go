//go:build tutorial

/*
Copyright The ORAS Authors.
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
	"fmt"
	"log"

	"github.com/oras-project/oras-go/v3/registry/remote/config"
)

func main() {
	configs, err := config.LoadConfigs()
	if err != nil {
		log.Fatal(err)
	}

	props, err := configs.RegistryProperties("registry.example.com/team/app:latest")
	if err != nil {
		log.Fatal(err)
	}

	builder, err := configs.ClientBuilder(config.ClientBuilderOptions{})
	if err != nil {
		log.Fatal(err)
	}

	repository, err := builder.NewRepository(props)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(repository.Reference())
}
