# go-inference-router

go-inference-router is the provider-neutral model context used by host
applications to address models without depending on provider-specific details.
The contract, the runtime, and the `llm.v1` protocol are deliberately
host-agnostic.

## Language

**Host Application**:
Any program that selects a Model Profile and issues model requests — an agent, a CLI, an editor plugin, a service. It integrates as a Go library or by launching the Router and speaking `llm.v1` over stdio.
_Avoid_: Client, consumer, or the name of any specific host application

**Model Profile**:
A configured, friendly identifier for one provider model and its invocation settings. Only Model Profiles are selectable.
_Avoid_: Alias, raw model

**Provider Definition**:
A named configuration describing how model requests reach one provider and how its credentials are resolved.
_Avoid_: Backend config, vendor config

**Provider Process**:
An isolated executable that adapts one provider SDK to the provider-neutral protocol.
_Avoid_: Plugin, driver binary

**Router**:
The model runtime that resolves Model Profiles, reports availability, and directs requests to Provider Processes or the built-in compatible provider.
_Avoid_: Manager, gateway

**Capability**:
A declared model behavior that callers may inspect before making a request, such as streaming, tools, or image input.
_Avoid_: Feature flag

**Installed Provider**:
A verified Provider Process artifact available in the local provider cache.
_Avoid_: Downloaded plugin
