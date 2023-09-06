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

package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials/trace"
)

const (
	basicAuthHost     = "localhost:2333"
	bearerAuthHost    = "localhost:666"
	exeErrorHost      = "localhost:500/exeError"
	jsonErrorHost     = "localhost:500/jsonError"
	noCredentialsHost = "localhost:404"
	traceHost         = "localhost:808"
	testUsername      = "test_username"
	testPassword      = "test_password"
	testRefreshToken  = "test_token"
)

var (
	errCommandExited       = fmt.Errorf("exited with error")
	errExecute             = fmt.Errorf("Execute failed")
	errCredentialsNotFound = fmt.Errorf(errCredentialsNotFoundMessage)
)

// testExecuter implements the Executer interface for testing purpose.
// It simulates interactions between the docker client and a remote
// credentials helper.
type testExecuter struct{}

// Execute mocks the behavior of a credential helper binary. It returns responses
// and errors based on the input.
func (e *testExecuter) Execute(ctx context.Context, input io.Reader, action string) ([]byte, error) {
	in, err := io.ReadAll(input)
	if err != nil {
		return nil, err
	}
	inS := string(in)
	switch action {
	case "get":
		switch inS {
		case basicAuthHost:
			return []byte(`{"Username": "test_username", "Secret": "test_password"}`), nil
		case bearerAuthHost:
			return []byte(`{"Username": "<token>", "Secret": "test_token"}`), nil
		case exeErrorHost:
			return []byte("Execute failed"), errExecute
		case jsonErrorHost:
			return []byte("json.Unmarshal failed"), nil
		case noCredentialsHost:
			return []byte("credentials not found"), errCredentialsNotFound
		case traceHost:
			traceHook := trace.ContextExecutableTrace(ctx)
			if traceHook != nil {
				if traceHook.ExecuteStart != nil {
					traceHook.ExecuteStart("testExecuter", "get")
				}
				if traceHook.ExecuteDone != nil {
					traceHook.ExecuteDone("testExecuter", "get", nil)
				}
			}
			return []byte(`{"Username": "test_username", "Secret": "test_password"}`), nil
		default:
			return []byte("program failed"), errCommandExited
		}
	case "store":
		var c dockerCredentials
		err := json.NewDecoder(strings.NewReader(inS)).Decode(&c)
		if err != nil {
			return []byte("program failed"), errCommandExited
		}
		switch c.ServerURL {
		case basicAuthHost, bearerAuthHost, exeErrorHost:
			return nil, nil
		case traceHost:
			traceHook := trace.ContextExecutableTrace(ctx)
			if traceHook != nil {
				if traceHook.ExecuteStart != nil {
					traceHook.ExecuteStart("testExecuter", "store")
				}
				if traceHook.ExecuteDone != nil {
					traceHook.ExecuteDone("testExecuter", "store", nil)
				}
			}
			return nil, nil
		default:
			return []byte("program failed"), errCommandExited
		}
	case "erase":
		switch inS {
		case basicAuthHost, bearerAuthHost:
			return nil, nil
		case traceHost:
			traceHook := trace.ContextExecutableTrace(ctx)
			if traceHook != nil {
				if traceHook.ExecuteStart != nil {
					traceHook.ExecuteStart("testExecuter", "erase")
				}
				if traceHook.ExecuteDone != nil {
					traceHook.ExecuteDone("testExecuter", "erase", nil)
				}
			}
			return nil, nil
		default:
			return []byte("program failed"), errCommandExited
		}
	}
	return []byte(fmt.Sprintf("unknown argument %q with %q", action, inS)), errCommandExited
}

func TestNativeStore_interface(t *testing.T) {
	var ns interface{} = &nativeStore{}
	if _, ok := ns.(Store); !ok {
		t.Error("&NativeStore{} does not conform Store")
	}
}

func TestNativeStore_basicAuth(t *testing.T) {
	ns := &nativeStore{
		&testExecuter{},
	}
	// Put
	err := ns.Put(context.Background(), basicAuthHost, auth.Credential{Username: testUsername, Password: testPassword})
	if err != nil {
		t.Fatalf("basic auth test ns.Put fails: %v", err)
	}
	// Get
	cred, err := ns.Get(context.Background(), basicAuthHost)
	if err != nil {
		t.Fatalf("basic auth test ns.Get fails: %v", err)
	}
	if cred.Username != testUsername {
		t.Fatal("incorrect username")
	}
	if cred.Password != testPassword {
		t.Fatal("incorrect password")
	}
	// Delete
	err = ns.Delete(context.Background(), basicAuthHost)
	if err != nil {
		t.Fatalf("basic auth test ns.Delete fails: %v", err)
	}
}

func TestNativeStore_refreshToken(t *testing.T) {
	ns := &nativeStore{
		&testExecuter{},
	}
	// Put
	err := ns.Put(context.Background(), bearerAuthHost, auth.Credential{RefreshToken: testRefreshToken})
	if err != nil {
		t.Fatalf("refresh token test ns.Put fails: %v", err)
	}
	// Get
	cred, err := ns.Get(context.Background(), bearerAuthHost)
	if err != nil {
		t.Fatalf("refresh token test ns.Get fails: %v", err)
	}
	if cred.Username != "" {
		t.Fatalf("expect username to be empty, got %s", cred.Username)
	}
	if cred.RefreshToken != testRefreshToken {
		t.Fatal("incorrect refresh token")
	}
	// Delete
	err = ns.Delete(context.Background(), basicAuthHost)
	if err != nil {
		t.Fatalf("refresh token test ns.Delete fails: %v", err)
	}
}

func TestNativeStore_errorHandling(t *testing.T) {
	ns := &nativeStore{
		&testExecuter{},
	}
	// Get Error: Execute error
	_, err := ns.Get(context.Background(), exeErrorHost)
	if err != errExecute {
		t.Fatalf("got error: %v, should get exeErr", err)
	}
	// Get Error: json.Unmarshal
	_, err = ns.Get(context.Background(), jsonErrorHost)
	if err == nil {
		t.Fatalf("should get error from json.Unmarshal")
	}
	// Get: Should not return error when credentials are not found
	_, err = ns.Get(context.Background(), noCredentialsHost)
	if err != nil {
		t.Fatalf("should not get error when no credentials are found")
	}
}

func TestNewDefaultNativeStore(t *testing.T) {
	defaultHelper := getDefaultHelperSuffix()
	wantOK := (defaultHelper != "")

	if _, ok := NewDefaultNativeStore(); ok != wantOK {
		t.Errorf("NewDefaultNativeStore() = %v, want %v", ok, wantOK)
	}
}

func TestNativeStore_trace(t *testing.T) {
	ns := &nativeStore{
		&testExecuter{},
	}
	// create trace hooks that write to buffer
	buffer := bytes.Buffer{}
	traceHook := &trace.ExecutableTrace{
		ExecuteStart: func(executableName string, action string) {
			buffer.WriteString(fmt.Sprintf("test trace, start the execution of executable %s with action %s ", executableName, action))
		},
		ExecuteDone: func(executableName string, action string, err error) {
			buffer.WriteString(fmt.Sprintf("test trace, completed the execution of executable %s with action %s and got err %v", executableName, action, err))
		},
	}
	ctx := trace.WithExecutableTrace(context.Background(), traceHook)
	// Test ns.Put trace
	err := ns.Put(ctx, traceHost, auth.Credential{Username: testUsername, Password: testPassword})
	if err != nil {
		t.Fatalf("trace test ns.Put fails: %v", err)
	}
	bufferContent := buffer.String()
	if bufferContent != "test trace, start the execution of executable testExecuter with action store test trace, completed the execution of executable testExecuter with action store and got err <nil>" {
		t.Fatalf("incorrect buffer content: %s", bufferContent)
	}
	buffer.Reset()
	// Test ns.Get trace
	_, err = ns.Get(ctx, traceHost)
	if err != nil {
		t.Fatalf("trace test ns.Get fails: %v", err)
	}
	bufferContent = buffer.String()
	if bufferContent != "test trace, start the execution of executable testExecuter with action get test trace, completed the execution of executable testExecuter with action get and got err <nil>" {
		t.Fatalf("incorrect buffer content: %s", bufferContent)
	}
	buffer.Reset()
	// Test ns.Delete trace
	err = ns.Delete(ctx, traceHost)
	if err != nil {
		t.Fatalf("trace test ns.Delete fails: %v", err)
	}
	bufferContent = buffer.String()
	if bufferContent != "test trace, start the execution of executable testExecuter with action erase test trace, completed the execution of executable testExecuter with action erase and got err <nil>" {
		t.Fatalf("incorrect buffer content: %s", bufferContent)
	}
}

// This test ensures that a nil trace will not cause an error.
func TestNativeStore_noTrace(t *testing.T) {
	ns := &nativeStore{
		&testExecuter{},
	}
	// Put
	err := ns.Put(context.Background(), traceHost, auth.Credential{Username: testUsername, Password: testPassword})
	if err != nil {
		t.Fatalf("basic auth test ns.Put fails: %v", err)
	}
	// Get
	cred, err := ns.Get(context.Background(), traceHost)
	if err != nil {
		t.Fatalf("basic auth test ns.Get fails: %v", err)
	}
	if cred.Username != testUsername {
		t.Fatal("incorrect username")
	}
	if cred.Password != testPassword {
		t.Fatal("incorrect password")
	}
	// Delete
	err = ns.Delete(context.Background(), traceHost)
	if err != nil {
		t.Fatalf("basic auth test ns.Delete fails: %v", err)
	}
}

// This test ensures that an empty trace will not cause an error.
func TestNativeStore_emptyTrace(t *testing.T) {
	ns := &nativeStore{
		&testExecuter{},
	}
	traceHook := &trace.ExecutableTrace{}
	ctx := trace.WithExecutableTrace(context.Background(), traceHook)
	// Put
	err := ns.Put(ctx, traceHost, auth.Credential{Username: testUsername, Password: testPassword})
	if err != nil {
		t.Fatalf("basic auth test ns.Put fails: %v", err)
	}
	// Get
	cred, err := ns.Get(ctx, traceHost)
	if err != nil {
		t.Fatalf("basic auth test ns.Get fails: %v", err)
	}
	if cred.Username != testUsername {
		t.Fatal("incorrect username")
	}
	if cred.Password != testPassword {
		t.Fatal("incorrect password")
	}
	// Delete
	err = ns.Delete(ctx, traceHost)
	if err != nil {
		t.Fatalf("basic auth test ns.Delete fails: %v", err)
	}
}

func TestNativeStore_multipleTrace(t *testing.T) {
	ns := &nativeStore{
		&testExecuter{},
	}
	// create trace hooks that write to buffer
	buffer := bytes.Buffer{}
	trace1 := &trace.ExecutableTrace{
		ExecuteStart: func(executableName string, action string) {
			buffer.WriteString(fmt.Sprintf("trace 1 start %s, %s ", executableName, action))
		},
		ExecuteDone: func(executableName string, action string, err error) {
			buffer.WriteString(fmt.Sprintf("trace 1 done %s, %s, %v ", executableName, action, err))
		},
	}
	ctx := context.Background()
	ctx = trace.WithExecutableTrace(ctx, trace1)
	trace2 := &trace.ExecutableTrace{
		ExecuteStart: func(executableName string, action string) {
			buffer.WriteString(fmt.Sprintf("trace 2 start %s, %s ", executableName, action))
		},
		ExecuteDone: func(executableName string, action string, err error) {
			buffer.WriteString(fmt.Sprintf("trace 2 done %s, %s, %v ", executableName, action, err))
		},
	}
	ctx = trace.WithExecutableTrace(ctx, trace2)
	trace3 := &trace.ExecutableTrace{}
	ctx = trace.WithExecutableTrace(ctx, trace3)
	// Test ns.Put trace
	err := ns.Put(ctx, traceHost, auth.Credential{Username: testUsername, Password: testPassword})
	if err != nil {
		t.Fatalf("trace test ns.Put fails: %v", err)
	}
	bufferContent := buffer.String()
	if bufferContent != "trace 2 start testExecuter, store trace 1 start testExecuter, store trace 2 done testExecuter, store, <nil> trace 1 done testExecuter, store, <nil> " {
		t.Fatalf("incorrect buffer content: %s", bufferContent)
	}
	buffer.Reset()
	// Test ns.Get trace
	_, err = ns.Get(ctx, traceHost)
	if err != nil {
		t.Fatalf("trace test ns.Get fails: %v", err)
	}
	bufferContent = buffer.String()
	if bufferContent != "trace 2 start testExecuter, get trace 1 start testExecuter, get trace 2 done testExecuter, get, <nil> trace 1 done testExecuter, get, <nil> " {
		t.Fatalf("incorrect buffer content: %s", bufferContent)
	}
	buffer.Reset()
	// Test ns.Delete trace
	err = ns.Delete(ctx, traceHost)
	if err != nil {
		t.Fatalf("trace test ns.Delete fails: %v", err)
	}
	bufferContent = buffer.String()
	if bufferContent != "trace 2 start testExecuter, erase trace 1 start testExecuter, erase trace 2 done testExecuter, erase, <nil> trace 1 done testExecuter, erase, <nil> " {
		t.Fatalf("incorrect buffer content: %s", bufferContent)
	}
}
