package decompose

import "testing"

func TestParseAndValidateAcceptsV2Result(t *testing.T) {
	t.Parallel()
	data := []byte(`{
		"mode":"structured_breakdown",
		"source_summary":"stagger work",
		"items":[{
			"title":"Prototype stagger",
			"type":"task",
			"domain_hint":"combat",
			"module_hint":"stagger",
			"summary":"Add stagger prototype",
			"details":"Prototype body-part stagger reactions.",
			"source_text":"Need stagger for infected.",
			"priority":"medium",
			"labels":["codex-generated"],
			"dependencies":[],
			"acceptance_criteria":[],
			"needs_clarification":[]
		}],
		"needs_clarification":[],
		"eventlog_summary":"created one item"
	}`)
	if _, err := ParseAndValidate(data); err != nil {
		t.Fatal(err)
	}
}

func TestParseAndValidateRejectsPlaneStyleResult(t *testing.T) {
	t.Parallel()
	data := []byte(`{
		"mode":"structured_breakdown",
		"source_summary":"old result",
		"items":[{
			"title":"Prototype stagger",
			"type":"task",
			"project":"Combat Framework",
			"module":"Stagger",
			"summary":"Add stagger prototype",
			"details":"Prototype body-part stagger reactions.",
			"source_text":"Need stagger for infected.",
			"priority":"medium",
			"labels":["codex-generated"],
			"dependencies":[],
			"acceptance_criteria":[],
			"needs_clarification":[]
		}],
		"needs_clarification":[],
		"eventlog_summary":"created one item"
	}`)
	if _, err := ParseAndValidate(data); err == nil {
		t.Fatal("Plane-style result unexpectedly accepted")
	}
}
