// Like provider/anthropicsdk, this driver is a separate module so the core
// module's zero-dependency promise survives: importing
// github.com/rivt-ai/go-inference-router never pulls a vendor SDK in.
module github.com/rivt-ai/go-inference-router/provider/openaisdk

go 1.26

require (
	github.com/rivt-ai/go-inference-router v0.0.0
	github.com/openai/openai-go v1.12.0
)

require (
	github.com/tidwall/gjson v1.14.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
)

// The core module is untagged pre-1.0. Drop this once it carries a version.
replace github.com/rivt-ai/go-inference-router => ../..
