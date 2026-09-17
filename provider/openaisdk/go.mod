// Like provider/anthropicsdk, this driver is a separate module so the core
// module's zero-dependency promise survives: importing
// github.com/rivt-ai/go-inference-router never pulls a vendor SDK in.
module github.com/rivt-ai/go-inference-router/provider/openaisdk

go 1.26

toolchain go1.26.6

require (
	github.com/openai/openai-go v1.12.0
	github.com/rivt-ai/go-inference-router v0.7.0
)

require (
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
)
