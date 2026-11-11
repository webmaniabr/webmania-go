#!/bin/sh
# Exemplo neutro de homologação. Sem WEBMANIA_LIVE=1 não compila nem faz requisições (saída 77).
# Compila o exemplo deste checkout com os módulos já resolvidos em go.sum.
set -eu
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH= cd -- "$here/../.." && pwd)
if [ "${WEBMANIA_LIVE:-}" != "1" ]; then printf '%s\n' '{"result":"blocked"}'; exit 77; fi
out=$(mktemp -d "${TMPDIR:-/tmp}/webmania-example.XXXXXX")
(cd "$root" && go build -o "$out/homologacao" ./examples/homologacao) >&2
exec "$out/homologacao"
