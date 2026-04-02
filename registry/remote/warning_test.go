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
	"bytes"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func Test_parseWarningHeader(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		want    WarningValue
		wantErr error
	}{
		{
			name:   "Valid warning",
			header: `299 - "This is a warning."`,
			want: WarningValue{
				Code:  299,
				Agent: "-",
				Text:  "This is a warning.",
			},
		},
		{
			name:   "Valid meaningless warning",
			header: `299 - " "`,
			want: WarningValue{
				Code:  299,
				Agent: "-",
				Text:  " ",
			},
		},
		{
			name:    "Multiple spaces in warning",
			header:  `299  -   "This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Leading space in warning",
			header:  ` 299 - "This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Trailing space in warning",
			header:  `299 - "This is a warning." `,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Warning with a non-299 code",
			header:  `199 - "This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Warning with a non-unknown agent",
			header:  `299 localhost:5000 "This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Warning with a date",
			header:  `299 - "This is a warning." "Sat, 25 Aug 2012 23:34:45 GMT"`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Invalid format",
			header:  `299 - "This is a warning." something strange`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Not a warning",
			header:  `foo bar baz`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "No code",
			header:  `- "This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "No agent",
			header:  `299 "This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "No text",
			header:  `299 -`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Empty text",
			header:  `299 - ""`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Unquoted text",
			header:  `299 - This is a warning.`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Single-quoted text",
			header:  `299 - 'This is a warning.'`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Back-quoted text",
			header:  "299 - `This is a warning.`",
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
		{
			name:    "Invalid quotes",
			header:  `299 - 'This is a warning."`,
			want:    WarningValue{},
			wantErr: errUnexpectedWarningFormat,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWarningHeader(tt.header)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("parseWarningHeader() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseWarningHeader() = %v, want %v", got, tt.want)
			}
		})
	}
}

func makeTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func warn(text string) Warning {
	return Warning{WarningValue: WarningValue{Code: 299, Agent: "-", Text: text}}
}

func TestNewWarningLogger_LogsOnce(t *testing.T) {
	var buf bytes.Buffer
	fn := NewWarningLogger("registry.example.com", makeTestLogger(&buf))

	fn(warn("deprecated API"))
	fn(warn("deprecated API")) // duplicate — must be suppressed

	if got := strings.Count(buf.String(), "deprecated API"); got != 1 {
		t.Errorf("expected 1 log line, got %d", got)
	}
}

func TestNewWarningLogger_DifferentWarningsAllLogged(t *testing.T) {
	var buf bytes.Buffer
	fn := NewWarningLogger("registry.example.com", makeTestLogger(&buf))

	fn(warn("first warning"))
	fn(warn("second warning"))

	out := buf.String()
	if !strings.Contains(out, "first warning") {
		t.Error("expected first warning to be logged")
	}
	if !strings.Contains(out, "second warning") {
		t.Error("expected second warning to be logged")
	}
}

func TestNewWarningLogger_IncludesRegistry(t *testing.T) {
	var buf bytes.Buffer
	fn := NewWarningLogger("myregistry.example.com", makeTestLogger(&buf))

	fn(warn("some notice"))

	if !strings.Contains(buf.String(), "myregistry.example.com") {
		t.Error("expected registry name in log output")
	}
}

func TestNewWarningLogger_IndependentInstances(t *testing.T) {
	// Two loggers for the same registry must deduplicate independently —
	// each has its own seen map, so both log the warning once.
	var buf bytes.Buffer
	logger := makeTestLogger(&buf)
	fn1 := NewWarningLogger("registry.example.com", logger)
	fn2 := NewWarningLogger("registry.example.com", logger)

	fn1(warn("shared warning"))
	fn2(warn("shared warning"))

	if got := strings.Count(buf.String(), "shared warning"); got != 2 {
		t.Errorf("expected 2 log lines (one per instance), got %d", got)
	}
}

func TestNewWarningLogger_Concurrent(t *testing.T) {
	var buf bytes.Buffer
	fn := NewWarningLogger("registry.example.com", makeTestLogger(&buf))

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(warn("concurrent warning"))
		}()
	}
	wg.Wait()

	if got := strings.Count(buf.String(), "concurrent warning"); got != 1 {
		t.Errorf("expected exactly 1 log line despite concurrent calls, got %d", got)
	}
}
