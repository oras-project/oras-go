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

package trace

import (
	"context"
	"testing"
)

func TestWithExecutableTrace(t *testing.T) {
	ctx := context.Background()

	t.Run("trace is nil", func(t *testing.T) {
		newCtx := WithExecutableTrace(ctx, nil)
		if newCtx != ctx {
			t.Errorf("expected context to be unchanged when trace is nil")
		}
	})

	t.Run("adding a new trace", func(t *testing.T) {
		trace := &ExecutableTrace{}
		newCtx := WithExecutableTrace(ctx, trace)
		if newCtx == ctx {
			t.Errorf("expected context to be changed when adding a new trace")
		}
		if got := ContextExecutableTrace(newCtx); got != trace {
			t.Errorf("expected trace to be added to context")
		}
	})

	t.Run("adding a new emtpy trace with existing trace", func(t *testing.T) {
		oldExecStartCount := 0
		oldExecDoneCount := 0
		oldTrace := &ExecutableTrace{
			ExecuteStart: func(executableName, action string) {
				oldExecStartCount++
			},
			ExecuteDone: func(executableName, action string, err error) {
				oldExecDoneCount++
			},
		}
		ctx = WithExecutableTrace(ctx, oldTrace)

		newTrace := &ExecutableTrace{}
		newCtx := WithExecutableTrace(ctx, newTrace)

		// verify new trace
		gotTrace1 := ContextExecutableTrace(newCtx)
		if gotTrace1 != newTrace {
			t.Error("expected new trace to be added to context")
		}

		// verify old trace
		gotTrace2 := ContextExecutableTrace(newCtx)
		if gotTrace2 == oldTrace {
			t.Errorf("expected old trace to be composed with new trace")
		}
		gotTrace2.ExecuteStart("oldExec", "oldAction")
		if want := 1; oldExecStartCount != want {
			t.Errorf("oldExecStartCount: got %d, expected: %v", oldExecStartCount, want)
		}
		gotTrace2.ExecuteDone("oldExec", "oldAction", nil)
		if want := 1; oldExecDoneCount != want {
			t.Errorf("oldExecDoneCount: got %d, expected: %v", oldExecDoneCount, want)
		}
	})

	t.Run("adding a new trace with existing trace", func(t *testing.T) {
		oldExecStartCount := 0
		oldExecDoneCount := 0
		oldTrace := &ExecutableTrace{
			ExecuteStart: func(executableName, action string) {
				oldExecStartCount++
			},
			ExecuteDone: func(executableName, action string, err error) {
				oldExecDoneCount++
			},
		}
		ctx = WithExecutableTrace(ctx, oldTrace)

		newExecStartCount := 0
		newExecDoneCount := 0
		newTrace := &ExecutableTrace{
			ExecuteStart: func(executableName, action string) {
				newExecStartCount++
			},
			ExecuteDone: func(executableName, action string, err error) {
				newExecDoneCount++
			},
		}
		newCtx := WithExecutableTrace(ctx, newTrace)

		// verify new trace
		gotTrace1 := ContextExecutableTrace(newCtx)
		if gotTrace1 != newTrace {
			t.Error("expected new trace to be added to context")
		}
		gotTrace1.ExecuteStart("newExec", "newAction")
		if want := 1; newExecStartCount != want {
			t.Errorf("newExecStartCount: got %d, expected: %v", newExecStartCount, want)
		}
		gotTrace1.ExecuteDone("newExec", "newAction", nil)
		if want := 1; newExecDoneCount != want {
			t.Errorf("newExecDoneCount: got %d, expected: %v", newExecDoneCount, want)
		}

		// verify old trace
		gotTrace2 := ContextExecutableTrace(newCtx)
		if gotTrace2 == oldTrace {
			t.Errorf("expected old trace to be composed with new trace")
		}
		gotTrace2.ExecuteStart("oldExec", "oldAction")
		if want := 2; oldExecStartCount != want {
			t.Errorf("oldExecStartCount: got %d, expected: %v", oldExecStartCount, want)
		}
		gotTrace2.ExecuteDone("oldExec", "oldAction", nil)
		if want := 2; oldExecDoneCount != want {
			t.Errorf("oldExecDoneCount: got %d, expected: %v", oldExecDoneCount, want)
		}
	})
}
