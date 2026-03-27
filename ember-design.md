# Ember: Intent-Driven Minimal Compute

## One-Line

An ember is a self-describing unit of compute that carries its intent, not its infrastructure.

## The Problem

Every execution model today is speculative. Containers provision a Linux userspace before knowing what the code needs. Lambda pre-bakes a language runtime. Firecracker boots a kernel. Even Cloudflare's Dynamic Workers instantiate a full V8 isolate. They all build the house before asking what room you need.

AI agents are about to generate enormous volumes of small, short-lived, single-purpose code. The execution infrastructure for this code should be proportional to the code's actual needs — not to the platform's worst-case assumptions.

## The Insight

**Intent is known before execution.** Either explicitly (an agent declares what it needs) or implicitly (static analysis discovers what the code actually uses). If you know the intent, you can synthesize exactly the execution surface required — nothing more — and tear it down when the computation completes.

The closest existence proof is eBPF: verified, JIT-compiled, capability-scoped code running at kernel speed with zero runtime overhead, because the verifier proved safety before the first instruction ran.

## Core Concepts

### Ember

A unit of compute. Code plus its discovered intent. Not a container, not a function, not a job. An ember doesn't know or care what executes it. It carries:

- **Code**: a computational payload (initially Go source → WASM)
- **Intent Manifest**: what the code actually needs, discovered by static analysis
  - Memory bound (e.g., 50MB max)
  - Compute bound (e.g., 10s wall clock)
  - Capabilities required (e.g., read from S3 path X, write to address Y)
  - Capabilities absent (e.g., no network, no filesystem, no concurrency)
  - Hardware affinity (e.g., needs SIMD, needs GPU — future)
- **Return address**: where the result goes

The manifest is not authored by a human. It is **extracted by Giri** from static analysis of the code's SSA form. The code tells you what it needs; you just have to listen.

### Hearth

The minimal agent that runs embers. A hearth sits on any surface where compute can happen — a cluster node, a laptop, a cloud instance, a Slurm job. It knows two things:

1. **What am I.** A capability fingerprint: cores, memory, available runtimes, network access, attached accelerators.
2. **Can I burn this.** Match the ember's intent manifest against local capabilities. Binary yes/no.

A hearth is not a daemon, not an orchestrator, not a scheduler. It's a function:

```
hearth(ember) → result | error
```

The hearth's job is to **synthesize the minimal execution surface** from the ember's intent, run the code, return the result, and clean up. Nothing persists.

### Giri (Verifier + Intent Extractor)

Giri is a Go SSA interpreter/analyzer that serves two roles:

1. **Verifier**: proves the code is safe — no use-after-free, no unbounded allocation, no capability violations, termination guarantee.
2. **Intent extractor**: discovers what the code actually uses and emits the intent manifest.

These are the same pass. Verification and intent extraction are two views of the same analysis. "This code doesn't use the network" is simultaneously a safety property and a capability declaration.

#### What Giri Discovers (Intent Surface)

| Analysis | Safety Property | Intent Signal |
|----------|----------------|---------------|
| Allocation tracking | No unbounded alloc | Memory bound |
| Reachability | No dead code | Minimal code footprint |
| I/O tracing | No unauthorized access | Capability set |
| Call graph | No unauthorized calls | Required helpers |
| Loop analysis | Termination guarantee | Compute bound |
| Concurrency analysis | No races | Goroutine requirements (or absence) |
| Import analysis | No unauthorized packages | Runtime dependencies |

#### The eBPF Parallel

| eBPF | Ember/Giri |
|------|------------|
| BPF bytecode | Go SSA |
| Verifier (fixed rules) | Giri (intent-derived rules) |
| Helper functions | Capability-scoped helpers |
| JIT to native | SSA → minimal WASM (→ native, future) |
| Run-to-completion | Ember burns and is gone |
| Maps for state | External state via return address |
| 11 registers, 512B stack | Execution surface shaped by intent |

Key difference: eBPF's verification rules are fixed by Linux. Giri's rules are derived from the intent. Each ember gets a bespoke verification pass.

## Architecture

```
                    ┌─────────────────────┐
                    │   Code Source        │
                    │  (agent, human, CI)  │
                    └──────────┬──────────┘
                               │
                               ▼
                    ┌─────────────────────┐
                    │       Giri          │
                    │                     │
                    │  Go source → SSA    │
                    │  SSA → verification │
                    │  SSA → intent       │
                    │  SSA → minimal WASM │
                    └──────────┬──────────┘
                               │
                         ┌─────┴─────┐
                         │   Ember   │
                         │           │
                         │  .wasm    │
                         │  .intent  │
                         │  .return  │
                         └─────┬─────┘
                               │
              ┌────────────────┼────────────────┐
              ▼                ▼                 ▼
        ┌──────────┐    ┌──────────┐     ┌──────────┐
        │  Hearth  │    │  Hearth  │     │  Hearth  │
        │ (laptop) │    │ (cluster)│     │ (cloud)  │
        └──────────┘    └──────────┘     └──────────┘
```

## Compilation Pipeline: Go → Minimal WASM

This is the critical path. Today, `GOOS=wasip1 GOARCH=wasm go build` produces ~2MB+ WASM because it includes the full Go runtime. The ember pipeline eliminates everything Giri proves unnecessary.

### Phase 1: Giri Analyzes SSA

```
Go source → go/ssa package → SSA form
Giri walks SSA:
  - Tracks all allocations (shadow memory)
  - Traces pointer provenance
  - Maps reachable functions
  - Identifies runtime features used/unused
  - Proves safety properties
  - Emits intent manifest
```

### Phase 2: Runtime Stripping (Intent-Driven)

Giri's analysis tells us which Go runtime subsystems are needed:

| If Giri proves... | Then strip... | Savings |
|-------------------|---------------|---------|
| No goroutines | Scheduler, goroutine stack management | ~200KB |
| No GC-managed heap escapes | Garbage collector | ~300KB |
| All allocs bounded, no escape | Stack growth preamble | ~50KB |
| No reflection | Type metadata, reflect package | ~400KB |
| No channels | Channel implementation | ~100KB |
| No defer/panic | Defer machinery, panic handling | ~50KB |
| No string formatting | fmt and related | ~200KB |
| No interfaces (or known set) | Interface dispatch tables | varies |

A pure computational ember (matrix multiply, sequence alignment, data transform) might strip all of these.

### Phase 3: SSA → WASM Lowering

Rather than compiling Go and then stripping, emit WASM directly from verified SSA:

```
ssa.BinOp(Add, i64, i64)  →  i64.add
ssa.Alloc(T, size=1024)   →  memory.grow + known offset in linear memory
ssa.Call(pure_func)        →  call $func_idx
ssa.Return(val)            →  return
```

No GC. No scheduler. No stack maps. Just the computation.

This is feasible because:
- Go SSA and WASM are both SSA-form
- Giri has already resolved all types and sizes
- Giri has already proved memory safety
- The verified subset of Go maps cleanly to WASM primitives

### Phase 4: Partial Evaluation of Runtime (Future)

For embers that need *some* runtime features (e.g., a small hash map, bounded concurrency), instead of including the full subsystem, specialize it:

```
Ember needs: map[string]int with max 1000 entries
Full Go runtime: general hash map with GC integration, growth, evacuation
Specialized: fixed-capacity open-addressing hash, no GC hooks, inlined
```

This is the Futamura projection applied: specialize the runtime interpreter with respect to the specific program.

## Execution Model

### The Ember Runtime

The ember runtime is not a WASM runtime. It is an **intent-driven execution synthesizer** that uses WASM bytecode as one intermediate representation.

The distinction matters:

- A WASM runtime asks: "here's a module, run it safely"
- The ember runtime asks: "here's an intent manifest, synthesize the minimum execution surface, run it, tear it down"

WASM is the IR for v1 because it's portable, well-specified, and has a clean mapping from Go SSA. But the intent manifest is the real input. The runtime is shaped by the manifest, not by the WASM spec. If the manifest says "pure arithmetic, 4KB memory, no imports," the execution surface that materializes might be almost nothing — a memory region and a tight loop of native instructions.

### Relationship to wazero

wazero (tetratelabs/wazero) is the best existing pure-Go WASM runtime. Apache 2.0. No CGO. Clean architecture. It solved real problems well:

**Study and borrow:**
- WASM binary decoding (well-specified, mechanical)
- Linear memory implementation (mmap patterns)
- arm64/amd64 code generation backends (the JIT)
- Function call ABI conventions

**Don't carry forward:**
- General-purpose module instantiation
- Full WASI implementation
- Import/export resolution machinery
- The assumption that every module needs the same execution infrastructure

The relationship is not a fork. A fork implies maintaining their thing with modifications. The ember runtime is a different thing that solves some of the same subproblems. The relationship is more like Rust to C++ — learned from it deeply, borrowed ideas freely, built something with a different premise.

wazero instantiates a full WASM module with tables, globals, element segments, all the spec machinery — regardless of whether the module uses any of it. The ember runtime synthesizes only what the manifest declares. For a pure computational ember, the execution surface might contain a fraction of what wazero would instantiate.

### Hearth Internals

```go
// Minimal hearth implementation sketch
type Hearth struct {
    Fingerprint Capabilities  // what this hearth can provide
}

type Capabilities struct {
    MaxMemory   uint64
    Cores       int
    HasNetwork  bool
    HasGPU      bool
    GPUBackend  GPUKind  // WebGPU, CUDA, none
    Arch        string   // amd64, arm64
    Extensions  []string // avx512, sve2, etc.
}

func (h *Hearth) CanBurn(e *Ember) bool {
    return h.Fingerprint.Satisfies(e.Intent)
}

func (h *Hearth) Burn(e *Ember) (Result, error) {
    // 1. Validate intent against fingerprint
    if !h.CanBurn(e) {
        return nil, ErrInsufficientCapability
    }

    // 2. Synthesize execution surface from intent
    //    - Allocate exactly the memory declared
    //    - Bind only the helpers declared
    //    - Set compute timeout from intent
    surface := h.Synthesize(e.Intent)

    // 3. Execute
    result := surface.Execute(e.Code)

    // 4. Tear down — surface is ephemeral
    surface.Destroy()

    return result, nil
}
```

The `Synthesize` call is where the ember runtime diverges from a WASM runtime. A general runtime would instantiate a standard WASM module. The ember runtime reads the intent manifest and constructs only what's needed:

```go
func (h *Hearth) Synthesize(intent Intent, code []byte) *Surface {
    s := &Surface{}

    // Memory: exactly what was declared, no more
    s.memory = allocateLinear(intent.MemoryPages)

    // Decode only the WASM sections this ember uses
    // (intent manifest tells us which instruction families are present)
    s.decoder = newSelectiveDecoder(code, intent.InstructionProfile)

    // Helpers: only bind requested capability functions
    // Everything else doesn't exist — not blocked, absent
    for _, cap := range intent.Capabilities {
        s.bindHelper(cap)
    }

    // If intent declares GPU compute, bind WebGPU dispatch
    if intent.NeedsGPU() {
        s.bindGPU(h.gpuDevice)
    }

    // Compile: JIT only the instruction handlers this ember uses
    // This is the partial evaluation — fuse the "runtime" with the code
    s.executor = compile(s.decoder, s.helpers)

    return s
}
```

### WebGPU as First-Class Capability

WebGPU is not bolted onto the ember runtime as an afterthought. It's a first-class execution target that the intent manifest can request.

The current GPU compute landscape requires choosing a vendor stack: CUDA (NVIDIA), ROCm (AMD), Metal (Apple), or OpenCL (fragmented). Each requires drivers, toolkits, and vendor-specific code. WebGPU abstracts all of them through a single API backed by Vulkan, Metal, or DirectX depending on platform.

For Ember, GPU compute is just another capability in the manifest:

```
intent:
  compute: parallel
  memory: 256MB
  gpu:
    workgroups: [64, 64, 1]
    shared_memory: 16KB
    capabilities: [float32]
```

The hearth resolves this to the local GPU via wgpu-native (Go bindings to the Rust WebGPU implementation). The ember's WASM includes a compute shader as a data section. The runtime dispatches it.

Three hearth targets for GPU:

1. **Browser hearth** — easiest, browsers already have WebGPU natively
2. **Native hearth** — wgpu-native provides the same API without a browser
3. **Direct hearth** (future) — emit Vulkan/Metal compute directly, skip WebGPU

This is potentially the most valuable artifact in the stack independent of Ember. A pure Go library that gives WASM modules access to GPU compute shaders via wgpu-native doesn't exist today. Anyone building WASM-based agents, scientific compute, data tools, or games would want it.

### Future: Fused Execution

The ultimate form of "strip down down down." Instead of a runtime interpreting or JIT-compiling a module, the ember runtime **partially evaluates itself against a specific ember** to produce a fused executor.

```
ember runtime (general: WASM decode + JIT + memory + helpers)
  + specific ember (verified, intent-profiled)
  = fused executor (only the code paths this ember triggers)
    → Go compiler inlines and optimizes
      → approaches native speed with zero runtime overhead
```

The fused executor is neither a runtime nor a program. It's a single-purpose artifact that does one computation. Like an eBPF program after JIT — not interpreted, not running "on" something, just native instructions shaped by the original intent.

This is the Futamura projection: specializing an interpreter with respect to a specific program yields a compiler. Specializing further yields a compiler generator. The ember runtime, partially evaluated against an ember, yields a fused executor. Partially evaluated against a *class* of embers (from an intent pattern), it yields a specialized compiler for that pattern.

## Relationship to Existing Projects

### betty.codes → Ember

betty.codes inverts the Claude Code architecture: LLMs are stateless edge tools, context management is the core. When betty.codes emits code, that code is an ember candidate. Today it runs in... a subprocess. Tomorrow:

```
betty.codes generates Go function
  → Giri verifies + extracts intent
    → Ember is born
      → Nearest hearth burns it
        → Result returns to betty.codes
```

### SI Micro-Models → Giri

The structured intent micro-models generate Go code targeting 90%+ first-pass success. Giri verifies that the generated code matches the structured intent that produced it. The SI model declares what it intended; Giri confirms the code matches.

### burstcore.dev → Hearth Mesh

staRburst, adder, ASBX provide cloud bursting. A hearth mesh is a natural extension: instead of bursting Slurm jobs to cloud, scatter embers to hearths. The hearth could be a Slurm prolog, a Lambda wrapper, or a standalone binary.

### CloudRamp → Ember Economics

The M/M/∞ argument says cloud delivers more research per dollar because there's no queue. Ember makes the unit of compute so small that the queueing model dissolves entirely. You're not submitting jobs to a queue. You're scattering embers. The utilization question disappears because the execution surface exists only during the burn.

### SafeArena → Giri Foundation → Ember Core

SafeArena's arenacheck was the kernel of Giri. Giri generalized from arena-only to full UB detection. At v0.92 with 170+ stdlib intercepts, Giri already has the complete SSA walking and shadow memory infrastructure. Ember doesn't rebuild this — it refactors Giri into a shared core and adds a WASM emission backend alongside the existing interpretation backend.

## Practical Use Cases

### Agent Tool Execution (Agenkit)

Every agent framework today executes tool code in the host process (unsafe) or shells out to a subprocess (slow). Ember provides a third option: agent declares action → action compiles to ember → ember burns in verified sandbox → result returns. Agenkit's 18 patterns across 6 languages could each have an ember execution backend. The agent never trusts the code it generated because the code is verified and isolated.

This is what Cloudflare Dynamic Workers sell, but portable and open. No vendor, no edge network, no account.

### betty.codes Code Validation

betty generates Go code. Today, safety = trusting the LLM. With Ember: betty emits code → Giri verifies → passes = burn it → fails = feed verification errors back to LLM for another pass. The SI micro-model's "90% first-pass success" becomes provable, not statistical. The verification loop is the compile loop.

### WebGPU Compute via WASM

WASM has first-class access to WebGPU compute shaders. An ember with GPU intent could:

```
intent: "parallel computation on these arrays"
  → Giri verifies: pure computation, bounded memory, no I/O
    → emit WASM with WebGPU compute shader dispatch
      → hearth runs it on ANY GPU
```

No CUDA. No ROCm. No driver stack. WebGPU abstracts the hardware — Vulkan on Linux, Metal on Mac, DirectX on Windows. The ember doesn't know what GPU it's on.

For research computing: a PI with a MacBook has GPU compute. A student with an AMD card has GPU compute. No driver installation, no CUDA toolkit, no nvidia-runtime container.

A browser-based hearth is the easiest first GPU target (browsers already have WebGPU). A native hearth uses wgpu-native Go bindings.

### Queryabl Query Execution

User submits a coordinate-range query → query logic compiles to ember → burns against data shard → result returns. No persistent query executor. No connection pool. Scales to zero naturally — nothing runs when there are no queries.

### CargoShip Data Transforms

S3 object needs transformation during transfer (format conversion, filtering, projection). That's an ember. Transform code runs verified and stripped during data movement, then gone. No ETL pipeline, no persistent workers.

### Research Computing ("Run This Analysis")

"Run this analysis on my data" is the most common request in research computing. Today: write Slurm script, request resources, wait in queue, manage environment. With Ember: paste analysis code → Giri verifies → ember scatters to available hearths → results return. Researcher never saw infrastructure. This makes the CloudRamp M/M/∞ argument concrete.

### CI Verification Pipeline

Giri already has a GitHub Action. Extend: `giri verify` proves safety → `ember build` emits minimal WASM → CI artifact is a verified ember. Build pipeline produces embers, not containers.

## Implementation Roadmap

### Phase 0: Giri Refactor — Extract Core (weeks)

**Goal**: Split Giri's SSA walking infrastructure from its detection logic so both Giri and Ember can share it.

Giri is at v0.92 with 87 commits, 256+ integration tests, 170+ stdlib intercepts, and phases 1-3 (core interpreter, unsafe.Pointer rules, concurrency verification) complete. The hard work is done. The refactor extracts it.

**Step 1: Visitor interface.** Extract the SSA walk loop from `pkg/interpreter/exec.go` into a visitor pattern:

```go
type SSAVisitor interface {
    VisitBinOp(instr *ssa.BinOp) error
    VisitAlloc(instr *ssa.Alloc) error
    VisitCall(instr *ssa.Call) error
    VisitStore(instr *ssa.Store) error
    // ... one per SSA instruction type
}
```

Giri's interpreter becomes one visitor implementation. Ember's emitter becomes another.

**Step 2: Core package.** Reorganize within the same module:

```
github.com/scttfrdmn/giri/
  core/
    ssa/loader/         ← ssautil, package loading (extracted from internal/)
    ssa/walker/         ← SSA instruction walk loop + visitor interface
    shadow/             ← shadow memory, provenance, allocation tracking
    analysis/           ← capability/reachability graph (new, from shadow data)
  pkg/
    detector/           ← violation detectors (unchanged)
    scheduler/          ← goroutine scheduling (unchanged)
    report/             ← output formatting (unchanged)
    interpreter/        ← now implements core/ssa/walker.SSAVisitor
  cmd/giri/             ← CLI (unchanged)
```

Ember will import `github.com/scttfrdmn/giri/core`. Promote to separate module later if needed.

**Step 3: Proof of concept.** Take `func add(a, b int) int { return a + b }`, run through the refactored walker with a trivial WASM-emitting visitor, produce minimal .wasm. Execute it with a bare-bones ember runtime prototype (minimal WASM decoder + interpreter, borrowing patterns from wazero's source). Compare size/startup vs `GOOS=wasip1 go build`.

### Phase 1: Intent Extractor (weeks)

A new visitor (or detector) that reads Giri's shadow memory and analysis data to emit an intent manifest:

- [ ] Runtime feature analysis: which Go runtime subsystems are reachable?
- [ ] Memory bound: upper bound on allocations from shadow memory
- [ ] I/O surface: what external resources are touched (from stdlib intercept data)
- [ ] Concurrency: does the code spawn goroutines? (from scheduler data)
- [ ] Call graph: complete set of reachable functions
- [ ] Manifest format: JSON initially, binary later

This is not new analysis. It's asking different questions of data Giri already collects.

### Phase 2: SSA → WASM Emitter (months)

A second visitor implementation that lowers verified SSA to WASM bytecode:

- [ ] Arithmetic/logic: `ssa.BinOp` → `i64.add`, `i64.mul`, etc.
- [ ] Locals: SSA values → WASM locals (SSA is already in register form)
- [ ] Functions: `ssa.Call` → `call $func_idx`
- [ ] Memory: `ssa.Alloc` → region in WASM linear memory (size known from analysis)
- [ ] Control flow: `ssa.If`/`ssa.Jump` → WASM `br`/`br_if`/`block`/`loop`
- [ ] Helpers: capability functions → WASM imports (host provides)

NOT a general-purpose Go compiler. Handles the verified subset only. Unsupported features are a verification error, not a compilation error.

### Phase 3: Ember Runtime + Hearth Binary (months)

The ember runtime is a purpose-built execution synthesizer, not a WASM runtime. Study wazero's internals (Apache 2.0) for WASM decoding, linear memory, and JIT techniques. Build a runtime that reads intent manifests and synthesizes minimal execution surfaces.

- [ ] WASM binary decoder (borrow patterns from wazero)
- [ ] Linear memory allocator (intent-bounded, no growth)
- [ ] Selective instruction dispatch (only handlers the ember uses)
- [ ] JIT compilation for amd64/arm64 (start with one, borrow from wazero's backend patterns)
- [ ] Helper binding (only capabilities from manifest, everything else absent)
- [ ] Capability fingerprinting (detect local resources)
- [ ] Intent matching (can I burn this?)
- [ ] Result delivery (stdout, HTTP callback, file, etc.)
- [ ] CLI: `ember build source.go` (verify + extract intent + emit WASM + manifest)
- [ ] CLI: `hearth burn ember.wasm` (synthesize surface + execute + tear down)

Single static Go binary for the hearth. No CGO for v1 (CGO comes with WebGPU in Phase 5).

### Phase 4: Integration (quarter)

- [ ] betty.codes emits embers
- [ ] Agenkit ember execution pattern
- [ ] Slurm prolog hearth (ember-aware job dispatch)
- [ ] burstcore hearth (scatter to cloud)
- [ ] Observability (ember lifecycle tracing)

### Phase 5: WebGPU Compute (quarter)

WebGPU as a first-class capability in the ember runtime, not an external binding.

- [ ] Intent manifest GPU fields: `gpu: true`, `parallel_compute: true`, workgroup size, shared memory
- [ ] wgpu-native Go bindings (CGO — this is where CGO enters the stack)
- [ ] Compute shader as WASM data section, dispatched by runtime
- [ ] Browser hearth (simplest GPU path — browsers already have WebGPU)
- [ ] Native hearth with wgpu-native
- [ ] Benchmark vs. equivalent CUDA implementation
- [ ] Standalone Go+WebGPU library (valuable independent of Ember — this doesn't exist today)

### Phase 6: Partial Evaluation / Fused Execution (research)

The Futamura projection applied: specialize the ember runtime against a specific ember to produce a fused executor.

- [ ] Profile which runtime code paths a specific ember triggers
- [ ] Tree-shake the runtime: dead-code-eliminate untriggered instruction handlers
- [ ] Generate fused artifact: single Go function that IS the ember's execution
- [ ] Benchmark fused vs. general runtime vs. native Go compilation
- [ ] Explore: can the fused executor be emitted as a static binary? (ember becomes its own runtime)

### Phase 7: Hardware Targeting (research)

- [ ] Intent-driven compilation to native (Graviton SVE2, AVX-512, etc.)
- [ ] Hearth as JIT compiler from intent to metal
- [ ] Zero-runtime execution for pure computational embers

## Design Principles

1. **Intent first.** The code tells you what it needs. Listen. The manifest is discovered, not authored.
2. **Subtract, don't add.** Every layer must justify its existence against the intent. The history of compute infrastructure is accretion. Ember goes the other way.
3. **No landlord.** Ember runs anywhere with a hearth. No vendor, no platform, no account. This is what distinguishes Ember from Cloudflare Dynamic Workers, AWS Lambda, and every other execution service.
4. **Verify then run.** Safety comes from proof, not from a sandbox. If Giri passes it, run it bare. This is the eBPF lesson: the verifier eliminates the need for runtime safety checks.
5. **Ephemeral by default.** Nothing persists. The execution surface exists only during the burn. The ember, the surface, the result pathway — all gone.
6. **Borrow, don't fork.** Learn from wazero, eBPF, V8 isolates. Lift techniques where licenses allow. But build a purpose-built runtime shaped by intent, not a modified general-purpose runtime.
7. **Progressive depth.** v1 uses WASM as IR with a purpose-built runtime. v2 partially evaluates into fused executors. v3 compiles to bare metal. Same ember, deeper materialization. Same manifest, different synthesis.

## Open Questions

- **Ember format**: Single self-describing artifact (WASM + manifest)? Or separate files? The manifest must travel with the code.
- **Networking**: How does a hearth discover embers? Push (scatter)? Pull (poll)? For v1, probably just CLI: `ember build` then `hearth burn`.
- **State**: Embers are stateless, but chains of embers need to pass results. Protocol? Maybe just stdin/stdout for v1.
- **Multi-ember**: Does an ember ever spawn sub-embers? Or is that always the agent's job? Probably the latter — embers are atoms.
- **Trust**: Who signs embers? Does the Giri verification proof travel with the ember? Could be a hash of the verified SSA.
- **Debugging**: How do you debug an ember that's been stripped to nothing? Giri's verbose mode (`-v`) shows every SSA instruction — that's probably the debugger.
- **Error model**: What happens when an ember fails mid-burn? The run-to-completion model says it shouldn't, because Giri proved termination. But helpers can fail (S3 read timeout). Return error + partial result?
- **Language scope**: v1 is Go → WASM. Could other languages target the ember format? Any language with an SSA form could potentially feed through a Giri-like verifier. But Go first.
- **wgpu-native integration**: Pure Go bindings to wgpu-native via CGO? Or a separate sidecar process? CGO adds a build dependency but is faster. Could offer both.
- **Ember runtime licensing**: Apache 2.0 to match Giri and wazero? This enables maximum adoption.

## Intellectual Lineage

Ember is not built in a vacuum. It draws from specific prior art, and the relationship to each influence is deliberate.

| Influence | What we take | What we leave behind |
|-----------|-------------|---------------------|
| **eBPF** | Verify-then-run model, run-to-completion, helper-based capabilities, JIT shaped by program | Fixed verification rules, kernel-only execution, Linux-only |
| **Cloudflare Dynamic Workers** | Proof that agent-generated code execution is a real market, isolate-per-request model, "code mode" (code as intent) | Proprietary edge, V8 dependency, vendor lock-in |
| **wazero** | WASM binary decoding, linear memory patterns, arm64/amd64 JIT techniques, pure-Go philosophy | General-purpose module instantiation, full WASI, assumption that every module is the same shape |
| **Miri** | Interpretive verification against shadow memory, provenance tracking, the idea that a verifier is also an analyzer | Rust-specific, no compilation output, no intent extraction |
| **Futamura projections** | Partial evaluation of interpreters yields specialized compilers, runtime + program → fused executor | Theoretical framing (we build the practical system) |
| **GPU shaders** | The execution model IS an ember — declare intent ("for each pixel, do this"), hardware synthesizes execution, done | Graphics-specific, vendor-specific shader languages |
| **TinyGo** | Go → WASM without full runtime by restricting features | Restrictions are syntactic and conservative; ours are semantic and precise (Giri proves what's unused, not what's forbidden) |

### The SafeArena → Giri → Ember Arc

This project has a clear evolutionary path:

1. **SafeArena** (2024): Runtime wrappers for Go arena safety. Discovered that arena provenance tracking requires SSA-level analysis. Built `arenacheck` — an SSA-based static analyzer.

2. **Giri** (2024-2025): Generalized arenacheck into a full SSA interpreter with shadow memory. Went from arena-only to universal UB detection. 92 releases, 170+ stdlib intercepts. Discovered that the same analysis that proves safety also reveals what the code actually needs.

3. **Ember** (2025+): The realization that Giri's safety analysis is also an intent analysis, and that intent can drive compilation to minimal execution surfaces. Verification and compilation are the same pass. The verifier is also the compiler's optimization oracle.

Each step was motivated by a concrete problem (arena safety → general UB → minimal execution), and each step reused and extended the previous work rather than starting fresh.

## Name

**Ember**: small, hot, brief, gone. A self-contained spark of computation that carries exactly enough heat to do its work and nothing more.

**Hearth**: the surface where embers land. Provides tinder (capabilities) but doesn't control the flame.

**Giri** (existing): Go IR Interpreter. The verifier and intent extractor. Grew from SafeArena. Named after the Japanese concept of duty/obligation — the code's obligation to declare its true nature.

**Burn**: the act of execution. An ember burns on a hearth and produces a result.

**Ember Runtime**: the purpose-built execution synthesizer inside the hearth. Not a WASM runtime — an intent-driven execution surface generator. Inspired by wazero's engineering, built with a different premise.
