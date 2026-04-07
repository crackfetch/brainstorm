package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/crackfetch/brainstorm/workflow"
)

func TestPromptContent_NotEmpty(t *testing.T) {
	if promptContent() == "" {
		t.Fatal("promptContent() returned empty string")
	}
}

// TestPromptContent_DocumentsAllEvalTypes uses reflection on the EvalAssert
// struct to ensure every yaml-tagged field is documented in the prompt.
// If someone adds a new eval type to types.go, this test fails until they
// document it in agent.md.
func TestPromptContent_DocumentsAllEvalTypes(t *testing.T) {
	content := promptContent()
	skip := map[string]bool{"label": true, "timeout": true} // metadata, not assertion types

	rt := reflect.TypeOf(workflow.EvalAssert{})
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("yaml")
		yamlName := strings.Split(tag, ",")[0]
		if skip[yamlName] {
			continue
		}
		if !strings.Contains(content, yamlName+":") {
			t.Errorf("eval type %q (from EvalAssert.%s) is not documented in the agent prompt",
				yamlName, rt.Field(i).Name)
		}
	}
}

// TestPromptContent_DocumentsAllStepTypes uses reflection on the Step struct
// to ensure every yaml-tagged field is documented in the prompt.
// If someone adds a new step type to types.go, this test fails until they
// document it in agent.md.
func TestPromptContent_DocumentsAllStepTypes(t *testing.T) {
	content := promptContent()
	skip := map[string]bool{"label": true, "optional": true} // control flow, not step types

	rt := reflect.TypeOf(workflow.Step{})
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("yaml")
		yamlName := strings.Split(tag, ",")[0]
		if skip[yamlName] {
			continue
		}
		if !strings.Contains(content, yamlName) {
			t.Errorf("step type %q (from Step.%s) is not documented in the agent prompt",
				yamlName, rt.Field(i).Name)
		}
	}
}
