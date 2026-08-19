module github.com/rivt-ai/go-inference-router/examples/embedded-router

go 1.26

toolchain go1.26.6

require (
	github.com/rivt-ai/go-inference-router v0.6.0
	github.com/rivt-ai/go-inference-router/router v0.0.0
)

require golang.org/x/mod v0.39.0 // indirect

replace github.com/rivt-ai/go-inference-router => ../..

replace github.com/rivt-ai/go-inference-router/router => ../../router
