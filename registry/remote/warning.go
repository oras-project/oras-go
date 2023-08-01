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
	// headerWarning is the "Warning" header.
	// Reference: https://www.rfc-editor.org/rfc/rfc7234#section-5.5
	headerWarning = "Warning"

	// warnCode299 is the 299 warn-code.
	// Reference: https://www.rfc-editor.org/rfc/rfc7234#section-5.5
	warnCode299 = 299

	// warnAgentUnknown represents an unknown warn-agent.
	// Reference: https://www.rfc-editor.org/rfc/rfc7234#section-5.5
	warnAgentUnknown = "-"
)

// errUnexpectedWarningFormat is returned by parseWarningHeader when
// an unexpected warning format is encountered.
var errUnexpectedWarningFormat = errors.New("unexpected warning format")

// WarningHeader represents the value of the Warning header.
//
// References:
//   - https://github.com/opencontainers/distribution-spec/blob/v1.1.0-rc3/spec.md#warnings
//   - https://www.rfc-editor.org/rfc/rfc7234#section-5.5
type WarningHeader struct {
	// Code is the warn-code.
	Code int
	// Agent is the warn-agent.
	Agent string
	// Text is the warn-text.
	Text string
}

// Warning contains the value of the warning header and other information
// related to the warning.
type Warning struct {
	// Value is the value of the warning header.
	Value WarningHeader
	// Reference is the registry reference for which the warning is being
	// reported.
	Reference registry.Reference
}

// parseWarningHeader parses the value of the warning header into WarningHeader.
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
	if code != strconv.Itoa(warnCode299) {
		return WarningHeader{}, fmt.Errorf("%s: unexpected code: %w", header, errUnexpectedWarningFormat)
	}
	// validate agent
	if agent != warnAgentUnknown {
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
		Code:  warnCode299,
		Agent: warnAgentUnknown,
		Text:  text,
	}, nil
}

// handleWarningHeaders handle the values of warning headers.
func handleWarningHeaders(headers []string, reference registry.Reference, handleWarning func(Warning)) {
	for _, h := range headers {
		if wh, err := parseWarningHeader(h); err == nil {
			// ignore warnings in unexpected formats
			warning := Warning{
				Value:     wh,
				Reference: reference,
			}
			handleWarning(warning)
		}
	}
}
