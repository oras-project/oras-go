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
	"regexp"
)

var warningRegexp = regexp.MustCompile(`^299\s+-\s+"([^"]+)"`)

var errInvalidWarningFormat = errors.New("invalid warning format")

const (
	warningCode299      = 299
	warningAgentUnknown = "-"
)

type Warning struct {
	Code  int
	Agent string
	Text  string
}

func parseWarningHeader(header string) (Warning, error) {
	match := warningRegexp.FindStringSubmatch(header)
	if len(match) != 2 {
		return Warning{}, fmt.Errorf("%s: %w", header, errInvalidWarningFormat)
	}

	return Warning{
		Code:  warningCode299,
		Agent: warningAgentUnknown,
		Text:  match[1],
	}, nil
}
