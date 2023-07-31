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

package remote

import (
	"errors"
	"fmt"
	"regexp"

	"oras.land/oras-go/v2/registry"
)

const (
	headerWarning       = "Warning"
	warningCode299      = 299
	warningAgentUnknown = "-"
)

var warningRegexp = regexp.MustCompile(`^299\s+-\s+"([^"]+)"$`)

var errUnexpectedWarningFormat = errors.New("unexpected warning format")

type WarningHeader struct {
	Code  int
	Agent string
	Text  string
}

type Warning struct {
	WarningHeader
	Reference     registry.Reference
	RequestMethod string
	RequestPath   string
}

func parseWarningHeader(header string) (WarningHeader, error) {
	matches := warningRegexp.FindStringSubmatch(header)
	if len(matches) != 2 {
		return WarningHeader{}, fmt.Errorf("%s: %w", header, errUnexpectedWarningFormat)
	}

	return WarningHeader{
		Code:  warningCode299,
		Agent: warningAgentUnknown,
		Text:  matches[1],
	}, nil
}

// TODO: unit test
func parseWarningHeaders(headers []string) []WarningHeader {
	var result []WarningHeader
	for _, h := range headers {
		if wh, err := parseWarningHeader(h); err == nil {
			// ignore warnings in unexpected formats
			result = append(result, wh)
		}
	}
	return result
}
