module github.com/rivt-ai/go-inference-router/examples/embedded-router

go 1.26

require (
	github.com/rivt-ai/go-inference-router v0.0.0
	github.com/rivt-ai/go-inference-router/router v0.0.0
)

require (
	filippo.io/age v1.3.1 // indirect
	filippo.io/hpke v0.4.0 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/zalando/go-keyring v0.2.8 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
	golang.org/x/crypto v0.45.0 // indirect
	golang.org/x/mod v0.39.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
)

replace github.com/rivt-ai/go-inference-router => ../..

replace github.com/rivt-ai/go-inference-router/router => ../../router
