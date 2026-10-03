package inference

import (
	"encoding/json"
	"testing"
)

func TestDecisionValidation(t *testing.T) {
	valid := func() DecisionRequest {
		return DecisionRequest{State: json.RawMessage(`"state"`), Questions: map[string]DecisionQuestion{
			"q": {Instructions: json.RawMessage(`"question"`), Noul: &NoulQuestion{}},
		}}
	}
	for _, state := range []string{`"text"`, `{"n":123456789012345678901234567890}`, `[]`} {
		req := valid()
		req.State = json.RawMessage(state)
		if err := req.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []string{"", "null", "true", "12", "{", `[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,x"}}]}]`} {
		req := valid()
		req.State = json.RawMessage(state)
		if !IsKind(req.Validate(), KindInvalidRequest) {
			t.Fatalf("accepted state %s", state)
		}
	}
	for _, q := range []DecisionQuestion{
		{Instructions: json.RawMessage(`"q"`)},
		{Instructions: json.RawMessage(`"q"`), Choice: &ChoiceQuestion{}, Noul: &NoulQuestion{}},
		{Instructions: json.RawMessage(`"q"`), Choice: &ChoiceQuestion{Options: map[string]*string{"a": nil}}},
		{Instructions: json.RawMessage(`"q"`), Score: &ScoreQuestion{Levels: []string{"one"}}},
		{Noul: &NoulQuestion{}},
	} {
		req := valid()
		req.Questions["q"] = q
		if !IsKind(req.Validate(), KindInvalidRequest) {
			t.Fatalf("accepted question %+v", q)
		}
	}
	req := valid()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded DecisionRequest
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Validate() != nil {
		t.Fatalf("roundtrip %s: %v", data, err)
	}
	for _, data := range []string{`{"images":[]}`, `{"stream":true}`} {
		if err := json.Unmarshal([]byte(data), &decoded); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}
