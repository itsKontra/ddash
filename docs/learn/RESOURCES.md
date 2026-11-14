# ddash Plugin runtime Resources

## Knowledge

- [ADR 0001: Components run as go-plugin subprocesses](../adr/0001-go-plugin-subprocesses-lockstep-releases.md)
  Why there are processes, lockstep Releases and an import map. Use for: any "why is it built this way" question.
- [ADR 0002: The host routes Component calls from descriptors](../adr/0002-host-routes-component-calls-from-descriptors.md)
  Describe's FileDescriptorSet, the broker proxy, and Connect transcoding. Use for: router and proxy lessons.
- [Issue #19: Plugin runtime](https://github.com/itsKontra/ddash/issues/19) and [#12: Host architecture](https://github.com/itsKontra/ddash/issues/12)
  The full resolved decisions behind the ADRs (crash policy, install/update/uninstall, CSS discipline).
- [hashicorp/go-plugin README](https://github.com/hashicorp/go-plugin)
  The library under the subprocess model: handshake, SecureConfig, AutoMTLS, GRPCBroker. Use for: the handshake lesson.
- [go-plugin package docs](https://pkg.go.dev/github.com/hashicorp/go-plugin)
  API reference for ClientConfig, GRPCBroker, ServeConfig.
- [Connect protocol reference](https://connectrpc.com/docs/protocol/)
  The unary JSON wire format the router has to implement faithfully (ADR 0002).
- [dynamicpb](https://pkg.go.dev/google.golang.org/protobuf/types/dynamicpb)
  How the host builds message types from descriptors at runtime.
- [MDN: import maps](https://developer.mozilla.org/en-US/docs/Web/HTML/Reference/Elements/script/type/importmap)
  How the shell shares React and `@ddash/ui` with Plugin bundles.
- In-repo tests as executable docs: `sdk/lifecycle_test.go` (the smallest Component), `core/runtime_test.go`, `core/subprocess_test.go` (the real Release as subprocesses).

## Wisdom (Communities)

- [hashicorp/go-plugin GitHub Discussions/Issues](https://github.com/hashicorp/go-plugin/issues)
  Where maintainers of Terraform/Vault-style plugin hosts discuss broker, lifecycle and protocol-version edge cases.
- [Gophers Slack #general / r/golang](https://www.reddit.com/r/golang/)
  For design critique of the process-per-Component model.

## Gaps

- No written doc for the reconcile loop's state machine beyond code comments. A candidate for a future lesson, and maybe a doc.
