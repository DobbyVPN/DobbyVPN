# DobbyVPN functional tests

This directory contains the product functional suite. Product code and tests
share one repository and one revision. The suite is test tooling, not a
production runtime dependency.

## Ownership

torturer_contract defines scenarios, assertions, and result semantics.
torturer_checks supplies platform adapters and validation.
torturer_provider manages disposable Render test resources. The product
repository owns build and installation interfaces and GitHub workflows. The
private owner workspace supplies SSH transport and the guest lock/deadline
helper.

See [the functional contract](docs/contract.md) for coverage and
[product testing](../TESTING.md) for commands. Development rules are in
[the product instructions](../AGENTS.md).

## Hosted and local qualification

Hosted qualification installs the exact packages built by Release and runs the
canonical mini suite from the same source revision. It covers Linux, Windows,
macOS, and Android. The mini suite checks Linux CLI/service behavior, native
Windows and macOS UI behavior where the hosted runner supports it, and the
Android Compose UI with the real VPN service. The iOS Simulator has its own
single mini UI/lifecycle contract in the Test workflow.

The private Harness packages the product and suite from one selected worktree,
downloads a fresh owner profile, and runs the functional engine on disposable
VMs. It supports mini across the available platforms and full on interactive
Windows and macOS guests. Full opens the native frontend and exercises user
input, lifecycle actions, and UI-driven backend process-loss recovery.

The suite retains full diagnostic output for the current run and removes its
disposable test files after collection. It does not create separate evidence
archives.

Hosted VPN tests use one disposable Render VPN service and public connectivity
checks. Cleanup runs after qualification, including failed runs. Start a new
Release run to repeat qualification after cleanup.

## Import history

The suite was imported from DobbyVPN/Torturer commit
d71462247704de99ad302246ee9dc979da454d39. Its original license is retained in
[LICENSE](LICENSE).
