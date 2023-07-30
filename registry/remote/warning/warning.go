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

package warning

import (
	"errors"
	"fmt"
)

const warningFormat = "%d %s %q" //TODO: %s for text?

type Warning struct {
	Code  int
	Agent string
	Text  string
}

func parseWarningHeader(header string) (Warning, error) {
	var warning Warning
	n, err := fmt.Sscanf(header, warningFormat, &warning.Code, &warning.Agent, &warning.Text)
	if err != nil {
		return Warning{}, err
	}
	if n != 3 {
		return Warning{}, errors.New("failed to parse warning")
	}
	return warning, nil
}
