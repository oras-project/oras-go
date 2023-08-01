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
	"strconv"
	"strings"

	"oras.land/oras-go/v2/registry"
)

const (
	headerWarning       = "Warning"
	warningCode299      = 299
	warningAgentUnknown = "-"
)

var errUnexpectedWarningFormat = errors.New("unexpected warning format")

type WarningHeader struct {
	Code  int
	Agent string
	Text  string
}

type Warning struct {
	Value     WarningHeader
	Reference registry.Reference
}

func parseWarningHeader(header string) (WarningHeader, error) {
	if len(header) <= 8 || !strings.HasPrefix(header, `299 - "`) || !strings.HasSuffix(header, `"`) {
		// minimum header value: `299 - " "`
		return WarningHeader{}, fmt.Errorf("%s: %w", header, errUnexpectedWarningFormat)
	}
	parts := strings.SplitN(header, " ", 3)
	if len(parts) != 3 {
		return WarningHeader{}, fmt.Errorf("%s: %w", header, errUnexpectedWarningFormat)
	}

	code, agent, quotedText := parts[0], parts[1], parts[2]
	// validate code
	if code != strconv.Itoa(warningCode299) {
		return WarningHeader{}, fmt.Errorf("%s: unexpected code: %w", header, errUnexpectedWarningFormat)
	}
	// validate agent
	if agent != warningAgentUnknown {
		return WarningHeader{}, fmt.Errorf("%s: unexpected agent: %w", header, errUnexpectedWarningFormat)
	}
	// validate text
	text, err := strconv.Unquote(quotedText)
	if err != nil {
		return WarningHeader{}, fmt.Errorf("%s: unexpected text: %w: %v", header, errUnexpectedWarningFormat, err)
	}
	if len(text) == 0 {
		return WarningHeader{}, fmt.Errorf("%s: empty text: %w", header, errUnexpectedWarningFormat)
	}

	return WarningHeader{
		Code:  warningCode299,
		Agent: warningAgentUnknown,
		Text:  text,
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
