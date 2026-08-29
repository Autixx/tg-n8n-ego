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

func TestParseAndValidateAcceptsAdvisorResult(t *testing.T) {
	t.Parallel()
	data := []byte(`{
		"schema_version":"v2",
		"mode":"advisor",
		"source_summary":"User asks for UE5 planning advice.",
		"answer_markdown":"## Pipeline\nStart with project goals, then prototype movement.",
		"key_points":["Answer directly","Do not create backlog cards"],
		"suggested_next_actions":["Create tasks manually after reviewing the answer"],
		"needs_clarification":[],
		"eventlog_summary":"Answered as advisor."
	}`)
	result, err := ParseAndValidate(data)
	if err != nil {
		t.Fatal(err)
	}
	if result.AnswerMarkdown == "" || len(result.Items) != 0 {
		t.Fatalf("unexpected advisor result: %#v", result)
	}
}

func TestParseAndValidateRejectsAdvisorWithoutAnswer(t *testing.T) {
	t.Parallel()
	data := []byte(`{
		"mode":"advisor",
		"source_summary":"summary",
		"key_points":[],
		"suggested_next_actions":[],
		"needs_clarification":[],
		"eventlog_summary":"event"
	}`)
	if _, err := ParseAndValidate(data); err == nil {
		t.Fatal("advisor result without answer unexpectedly accepted")
	}
}
