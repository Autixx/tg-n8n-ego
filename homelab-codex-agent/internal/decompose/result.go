package decompose

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	modes      = map[string]bool{"abstract_idea": true, "structured_breakdown": true, "create_tasks": true, "advisor": true}
	itemTypes  = map[string]bool{"idea": true, "task": true, "bug": true, "research": true, "decision": true}
	domains    = map[string]bool{"core": true, "combat": true, "enemies": true, "biomorph": true, "world": true, "narrative": true, "items": true, "presentation": true, "tech": true, "parking": true}
	priorities = map[string]bool{"low": true, "medium": true, "high": true}
)

type Result struct {
	SchemaVersion        string   `json:"schema_version,omitempty"`
	Mode                 string   `json:"mode"`
	SourceSummary        string   `json:"source_summary"`
	Items                []Item   `json:"items,omitempty"`
	AnswerMarkdown       string   `json:"answer_markdown,omitempty"`
	KeyPoints            []string `json:"key_points,omitempty"`
	SuggestedNextActions []string `json:"suggested_next_actions,omitempty"`
	NeedsClarification   []string `json:"needs_clarification"`
	EventlogSummary      string   `json:"eventlog_summary"`
}

type Item struct {
	Title              string   `json:"title"`
	Type               string   `json:"type"`
	DomainHint         string   `json:"domain_hint"`
	ModuleHint         string   `json:"module_hint"`
	Summary            string   `json:"summary"`
	Details            string   `json:"details"`
	SourceText         string   `json:"source_text"`
	Priority           string   `json:"priority"`
	Labels             []string `json:"labels"`
	Dependencies       []string `json:"dependencies"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	NeedsClarification []string `json:"needs_clarification"`
}

func ParseAndValidate(data []byte) (Result, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Result{}, fmt.Errorf("v2 result is not valid JSON: %w", err)
	}
	for _, field := range []string{"mode", "source_summary", "needs_clarification", "eventlog_summary"} {
		if _, ok := raw[field]; !ok {
			return Result{}, fmt.Errorf("v2 result field %q is required", field)
		}
	}
	var mode string
	if err := json.Unmarshal(raw["mode"], &mode); err != nil {
		return Result{}, fmt.Errorf("v2 result mode schema error: %w", err)
	}
	if mode == "advisor" {
		if _, ok := raw["answer_markdown"]; !ok {
			return Result{}, errors.New(`v2 result field "answer_markdown" is required for advisor mode`)
		}
	} else if _, ok := raw["items"]; !ok {
		return Result{}, errors.New(`v2 result field "items" is required`)
	}
	if _, hasProject := raw["project"]; hasProject {
		return Result{}, errors.New("v2 result must not contain legacy project field")
	}
	if _, hasModule := raw["module"]; hasModule {
		return Result{}, errors.New("v2 result must not contain legacy module field")
	}

	var result Result
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Result{}, fmt.Errorf("v2 result schema error: %w", err)
	}
	if mode != "advisor" {
		var rawItems []map[string]json.RawMessage
		if err := json.Unmarshal(raw["items"], &rawItems); err != nil {
			return Result{}, fmt.Errorf("v2 result items schema error: %w", err)
		}
		for index, item := range rawItems {
			for _, field := range []string{"title", "type", "domain_hint", "module_hint", "summary", "details", "source_text", "priority", "labels", "dependencies", "acceptance_criteria", "needs_clarification"} {
				if _, ok := item[field]; !ok {
					return Result{}, fmt.Errorf("v2 result item %d field %q is required", index+1, field)
				}
			}
		}
	}
	if err := Validate(result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func Validate(result Result) error {
	if result.Mode == "" {
		return errors.New("v2 result mode is required")
	}
	if !modes[result.Mode] {
		return errors.New("v2 result mode is invalid")
	}
	if result.NeedsClarification == nil {
		return errors.New("v2 result needs_clarification is required")
	}
	if result.Mode == "advisor" {
		if result.AnswerMarkdown == "" {
			return errors.New("v2 result answer_markdown is required for advisor mode")
		}
		if result.KeyPoints == nil {
			return errors.New("v2 result key_points is required for advisor mode")
		}
		if result.SuggestedNextActions == nil {
			return errors.New("v2 result suggested_next_actions is required for advisor mode")
		}
		return nil
	}
	for index, item := range result.Items {
		if !itemTypes[item.Type] {
			return fmt.Errorf("v2 result item %d type is invalid", index+1)
		}
		if !domains[item.DomainHint] {
			return fmt.Errorf("v2 result item %d domain_hint is invalid or missing", index+1)
		}
		if !priorities[item.Priority] {
			return fmt.Errorf("v2 result item %d priority is invalid", index+1)
		}
		if item.Labels == nil || item.Dependencies == nil || item.AcceptanceCriteria == nil || item.NeedsClarification == nil {
			return fmt.Errorf("v2 result item %d array fields are required", index+1)
		}
	}
	return nil
}
