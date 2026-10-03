package openaicompat

// JevBaseURL is TypeSafe's hosted System One API.
const JevBaseURL = "https://api.typesafe.ai/v1"

// JevModel is TypeSafe's documented alias for the current Jev model.
const JevModel = "jev-latest"

// JevConfig returns a decisions-only Config for TypeSafe Jev. Adjust the
// returned value (BaseURL for a Jev-compatible server, HTTPClient, more
// DecisionModels) before passing it to New.
func JevConfig(apiKey string) Config {
	return Config{
		Name: "jev", BaseURL: JevBaseURL, APIKey: apiKey,
		DecisionsOnly: true, DecisionModels: []string{JevModel},
	}
}
