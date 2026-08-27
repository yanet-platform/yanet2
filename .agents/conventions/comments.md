# Comment conventions

Loaded on demand by the agent writing or reviewing any language; this is the single source for comment structure. The per-language conventions defer to this file.

Every comment — `//`, `#`, `/* */`, `///` doc, `<!-- -->`, or any other line/block comment in any language — uses the two-part shape below. A comment that fits entirely in the brief has no detailed block; a comment that needs more always separates the two with one blank line.

```
<brief>

<detailed>
```

- **Brief**: 1–2 lines, the why or the contract, not a restatement of the code. State the invariant or the reason the code is non-obvious; if the line only paraphrases the statement beneath it, delete the comment.
- **Blank line**: exactly one, separating brief from detailed. Absent when there is no detailed block. Never two.
- **Detailed**: up to 6–8 lines, soft — the ceiling is a guideline, not a gate. Preconditions, callers, failure modes, links to the design; still prose, not a bulleted spec. If it grows past the ceiling, the comment is documenting a function that should be split or a type that should be named.
- **One comment per block**: a single brief may stand alone; a detailed block is never written without its brief above it. Do not stack several detached paragraphs under one code span — fold them into one brief + one detailed, or split the code.
- **No code identifiers in prose**: a comment states intent, invariants, and contracts in domain terms. It must not name internal functions, methods, variables, fields, types, or local symbols — that restates the code and rots on every rename. Write "a failed update leaves the prior config intact", not "if UpdateConfig returns err, m.config is unchanged".

  Two narrow exceptions. (a) A doc comment opens with its own symbol's name (godoc / rustdoc synopsis requirement) — the name of the item it documents is allowed there and only there. (b) A cross-reference to another symbol (`// like Sort but stable`, `// see ReclaimStaleLayers`) is allowed only when the relationship to that symbol *is* the invariant; otherwise describe the behavior. External contract identifiers that are themselves the contract under test — a gRPC `codes.*` value (`NotFound`, `InvalidArgument`), an HTTP status — are part of the public error contract, not internal detail, and stay in the comment when the comment's point is that contract; prefer the domain term ("not found") only where no lossy wording and no ambiguity result.

- **No comment is a substitute for a name**: if the brief is "this returns the foo", rename the symbol instead. Comments record what the code cannot say about itself: intent, constraints, history, cross-references.
- **No alternatives not taken**: state the invariant the code upholds, not what a rejected implementation would have done — an argument against a version no reader will see is stale the day it lands. Write "a refused item does not hold up the rest of the batch", not "stopping there would strand everything behind it". The rejected design belongs in the commit message or the issue.
- **Reflow and width**: the language formatter (`clang-format` `ReflowComments`, `rustfmt` `wrap_comments`, `gofmt`'s none) may rewrap long lines; author the brief and each detailed line to stay within the language's column limit so a format pass does not alter structure. A reflow must never merge the blank line away or join the brief into the detailed block.
- **Doc comments** (`///`, `/** */`, `//`, godoc) follow the same shape: the brief is the synopsis the doc tool prints; the detailed block is the body. For godoc the blank line is what separates the synopsis from the rest, which `go doc` relies on.
- **CGO is exempt**: the C preamble of a `import "C"` file — `#cgo` pragmas, `#include` directives, C prototypes, and any C `/* */`/`//` inside that block — is C, not Go prose, and is not subject to this shape. The rule applies to Go-side `//` comments in the same file as normal.
