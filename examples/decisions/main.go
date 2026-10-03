// Command decisions calls a System One backend such as a decision-capable
// llama.cpp server: go run ./examples/decisions -url http://localhost:8080/v1 -model laya
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	inference "github.com/rivt-ai/go-inference-router"
	"github.com/rivt-ai/go-inference-router/provider/openaicompat"
)

func main() {
	baseURL := flag.String("url", "http://localhost:8080/v1", "System One server base URL")
	model := flag.String("model", "laya", "decision model ID")
	flag.Parse()
	client := openaicompat.New(openaicompat.Config{
		BaseURL: *baseURL, APIKey: os.Getenv("SYSTEMONE_API_KEY"),
		DecisionModels: []string{*model},
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	response, err := client.Decide(ctx, inference.DecisionRequest{
		Model: *model, State: json.RawMessage(`{"message":"I was charged twice."}`),
		Questions: map[string]inference.DecisionQuestion{
			"team": {
				Instructions: json.RawMessage(`"Which team should handle this?"`),
				Choice: &inference.ChoiceQuestion{Options: map[string]*string{
					"billing": nil, "shipping": nil, "technical": nil,
				}},
			},
			"urgent": {
				Instructions: json.RawMessage(`"Does this need immediate attention?"`),
				Noul:         &inference.NoulQuestion{},
			},
			"priority": {
				Instructions: json.RawMessage(`"How urgent is this?"`),
				Score:        &inference.ScoreQuestion{Levels: []string{"can wait", "today", "right now"}},
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(data))
}
