# Development tests

Run SDK commands inside a local Docker container with Go 1.26 or newer. The public suite contains synthetic HTTP/TLS fixtures, schema cases, structural file fixtures and typed consumers. It does not require real API credentials.

Inside the container, use `go test -race -count=1 ./...`. `scripts/qualify.py --output /work/check` builds a fresh module proxy archive and verifies installation in an isolated consumer. Use a fresh output directory outside the source tree; the script refuses one that holds other files, since it recreates its own subdirectories there. The script includes only public SDK source, documentation, examples and test inputs in its archive.

To check generated sources, mount a checkout of the OpenAPI repository at `/contracts` and run `python3 scripts/generate.py --source /contracts --check` inside the container. The generator verifies the pinned `contract_digest` of the contract modules.

Fixture files and regression assertions are maintained test inputs. Logs, receipts, module caches and archives are generated locally and must remain outside the repository. A source cleanup changes the archive identity; rebuild it and record a new checksum before distributing it.

## Testes a partir da fonte

```sh
go test -race -count=1 ./...
```

A suíte usa fixtures HTTP/TLS sintéticas e não exige credenciais reais. Detalhes em [docs/TESTING.md](TESTING.md).
