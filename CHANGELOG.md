# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-03-26

### Added
- `core/ssa/loader`: loads Go source into SSA form via `golang.org/x/tools/go/ssa`
- `core/ssa/walker`: `Visitor` interface (7 methods) + `BaseVisitor` + `Walker`; deterministic alphabetical member traversal
- `core/intent`: `Manifest` struct with JSON serialization; detects goroutines, GC, channels, defer, panic, reflection; computes `RuntimeStrips`
- `core/shadow`: minimal allocation tracker (stack vs heap bounds)
- `pkg/analyzer`: `Visitor` implementation extracting `IntentManifest` from SSA
- `pkg/emitter/wasm`: `Visitor` implementation lowering SSA arithmetic to WASM binary; self-contained LEB128 + section encoder
- `pkg/hearth`: manifest-gated execution synthesizer backed by wazero (pure Go, Apache 2.0)
- `cmd/ember`: CLI with `ember build` subcommand producing `.wasm` + `.intent` files
- End-to-end pipeline: `Add(3, 4) int → 7` in 47 bytes of WASM, 22 tests passing

[Unreleased]: https://github.com/scttfrdmn/ember/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/scttfrdmn/ember/releases/tag/v0.1.0
