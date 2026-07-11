# Supported Languages

CCG registers these Tree-sitter walkers at runtime:

| Language | Extensions |
| -------- | ---------- |
| Go | `.go` |
| Python | `.py` |
| TypeScript | `.ts`, `.tsx` |
| Java | `.java` |
| Ruby | `.rb` |
| JavaScript | `.js`, `.jsx`, `.mjs`, `.cjs` |
| C | `.c`, `.h` |
| C++ | `.cpp`, `.cc`, `.cxx`, `.hpp`, `.hh`, `.hxx` |
| Rust | `.rs` |
| Kotlin | `.kt`, `.kts` |
| PHP | `.php` |
| Lua/Luau | `.lua`, `.luau` |

`internal/core/runtime.go` (`BuildWalkers`) is the implementation source of truth. When changing language support, update this reference, README language claims, and parser tests in the same change.
