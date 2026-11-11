# Detalhes técnicos de integração

Este documento reúne os detalhes técnicos do SDK para Go que complementam o [README](../README.md): autenticação, URL base, limites de transporte, arquivos, relatórios e o tratamento do resultado fiscal. O texto é o mesmo que estava no README até esta versão.

## Escopo e disponibilidade

Integração de **Emissor Fiscal e Monitor Fiscal** em aplicações de servidor. O Emissor abrange NF-e/NFC-e na API1 e NFS-e, CT-e/CT-e OS, MDF-e, NFCom e DC-e na API2, com arquivos, eventos e as três operações gerenciais B2B públicas da API1. O Monitor mantém apenas as famílias de documentos e capacidades do seu contrato documentado.

Disponível no repositório público [github.com/webmaniabr/webmania-go](https://github.com/webmaniabr/webmania-go), com instalação a partir do clone (veja a seção Instalação). Ainda não há publicação em registro de pacotes; os nomes e versões abaixo identificam os pacotes gerados a partir desta fonte. Regras do provedor, aceitação fiscal real e plataformas adicionais permanecem com limitações explícitas.

## Visão geral para desenvolvedores

| Item | Detalhe |
|---|---|
| Status | Disponível no GitHub: versão `v0.1.0-alpha.5` do módulo `github.com/webmaniabr/webmania-go`, instalada a partir do clone do repositório (pasta de proxy local gerada pelo clone); ainda sem versão publicada no proxy público de módulos Go |
| Runtime | Go >= 1.26 (`go.mod`); dependências `github.com/santhosh-tekuri/jsonschema/v6` e `golang.org/x/text` |
| Clientes | `NewNfeClient` (NF-e/NFC-e API 1.0), `NewNfeFilesClient`, `NewResellerClient` (gerencial B2B API 1.0), `NewEmitterClient` (emissor API 2.0), `NewEmitterFilesClient`, `NewReportFilesClient` e `NewIssuerReportFilesClient` (relatórios atuais), `NewMonitorClient` (Monitor Fiscal) |
| Cobertura | 102 operações habilitadas de 103, em [docs/OPERATIONS.md](OPERATIONS.md); modelos tipados por módulo em `model/` |
| Erros | `*Error` (`errors.go`), com `Kind`, `Operation`, `Status`, `Outcome` e `CorrelationID` |
| Contrato | OpenAPI de [webmania-openapi](https://github.com/webmaniabr/webmania-openapi) fixado em [contracts.lock.json](../contracts.lock.json) pelo `contract_digest` e pelo SHA-256 de cada módulo |

### Autenticação

- **NF-e/NFC-e (API 1.0)** — `CompanyCredentials` (`ConsumerKey`, `ConsumerSecret`, `AccessToken`, `AccessTokenSecret`) é enviada nos cabeçalhos `X-Consumer-Key`, `X-Consumer-Secret`, `X-Access-Token` e `X-Access-Token-Secret`. `ResellerCredentials` usa os mesmos cabeçalhos no escopo de revenda.
- **API 2.0** — `IssuerCredentials` (emissor) e `MonitorCredentials` (Monitor) são enviadas como `Authorization: Bearer <token>`; são credenciais distintas.

O exemplo abaixo lê as credenciais com `os.Getenv` (`WEBMANIA_CONSUMER_KEY`, `WEBMANIA_CONSUMER_SECRET`, `WEBMANIA_ACCESS_TOKEN`, `WEBMANIA_ACCESS_TOKEN_SECRET`). Outros exemplos: [examples/status](../examples/status), [examples/monitor](../examples/monitor), [examples/homologation](../examples/homologation) (emissão em homologação de qualquer modelo a partir de um payload privado, somente com `--execute`, seguida de consulta e cancelamento; mesmas proteções do ciclo neutro, ver "Teste em homologação") e [examples/homologacao](../examples/homologacao) (ciclo neutro completo, descrito em "Teste em homologação").

## Requisitos

Go 1.26/1.27 em aplicações de servidor. Os testes desta versão não estabelecem compatibilidade com Windows ou mobile nem aceitação fiscal real.

Os downloads de relatório consomem a quota prevista no contrato ou registram o acesso mesmo usando GET. Se a requisição pode ter sido enviada e a resposta se perde, o erro conserva `Outcome == "unknown"`; a capacidade do relatório não vira referência de recuperação. Não há reenvio automático. Rejeições HTTP conhecidas continuam distintas de resultado incerto.

## Instalação

Clone o repositório público e gere a pasta de proxy local do módulo a partir do clone (o script só empacota o módulo; requer Python 3 e git):

```sh
git clone https://github.com/webmaniabr/webmania-go.git
python3 webmania-go/scripts/qualify.py --build-only --output "$PWD/webmania-go-proxy"
```

No consumidor:

```sh
go mod init minhaempresa.example/integracao
GOPROXY="file:///caminho/webmania-go-proxy/proxy,https://proxy.golang.org" \
GONOSUMDB=github.com/webmaniabr/webmania-go \
go get github.com/webmaniabr/webmania-go@v0.1.0-alpha.5
```

A exceção de checksum se restringe a este módulo, que ainda não tem versão publicada no proxy público de módulos Go; dependências públicas mantêm verificação normal. Não exige gerador.

## Primeira consulta

Configure somente a credencial do serviço escolhido, por instância, a partir de ambiente privado ou cofre de segredos. O exemplo não emite nem cancela documentos. Execute consultas apenas com conta e ambiente autorizados; uma origem de fixture controlada deve ser configurada explicitamente pela API do cliente.

```go
package main

import (
    "context"
    "fmt"
    "os"
    "time"
    wm "github.com/webmaniabr/webmania-go"
)

func main() {
    client, err := wm.NewNfeClient(wm.CompanyCredentials{
        ConsumerKey: os.Getenv("WEBMANIA_CONSUMER_KEY"),
        ConsumerSecret: os.Getenv("WEBMANIA_CONSUMER_SECRET"),
        AccessToken: os.Getenv("WEBMANIA_ACCESS_TOKEN"),
        AccessTokenSecret: os.Getenv("WEBMANIA_ACCESS_TOKEN_SECRET"),
    }, wm.Config{BaseURL: os.Getenv("WEBMANIA_BASE_URL")})
    if err != nil { fmt.Println(err); return }
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    result, err := client.GetSefazStatus(ctx, wm.NfeGetSefazStatusParams{})
    if err != nil { fmt.Println(err); return }
    status, _ := result.Value.Status.Get()
    fmt.Println(status)
}
```

A construção do cliente não comprova habilitação da conta. Respostas de sucesso e conversões explícitas para JSON podem conter dados fiscais, credenciais novas ou URLs privadas. Exponha apenas os campos necessários à aplicação.

## Integração e limites

`value.Some(false)` e `value.Some[int64](../0)` enviam esses valores. O zero de `value.Field[T]` omite a propriedade; `value.Null[T]()` envia `null` quando o schema aceita. Use `value.String("123.4500")` para texto decimal e `value.Number(json.Number("123.4500"))` para número JSON exato. Não converta identificadores nem decimais por `float64`. Campos inteiros aceitam `1`, `1.0` e `1e0` quando representam o mesmo inteiro exato e cabem em `int64`; frações e estouro falham. A representação numérica recebida é preservada na serialização enquanto o valor tipado não muda. Nomes JSON são sensíveis a maiúsculas: `STATUS` e `UUID` permanecem extensões separadas de `status` e `uuid`. Uniões de formas diferentes expõem `value.Union[A,B]`, com `First`/`Second`. Os campos comuns de objetos compostos permanecem acessíveis diretamente.

`Extras map[string]json.RawMessage` preserva propriedades futuras e permite extensões onde o schema autoriza. A SDK recusa colisão com propriedades comuns/declaradas do modelo. Campos específicos de uma alternativa continuam tipados; uma forma adicional válida para outra alternativa fica em `Extras` quando não cabe nesse campo tipado, sem restringir a resposta inteira. A SDK também recusa chaves JSON duplicadas, valores não finitos, profundidade acima de 64, números JSON com mais de 512 caracteres ou expoente fora de ±10000 e payload acima do limite configurado. Respostas preservam propriedades novas e estados textuais novos quando o contrato permite. O validador executa JSON Schema 2020-12, composições, condicionais, formatos e extensões de limites decimais exatos; não calcula impostos nem substitui regras do provedor que só aparecem como orientação textual.

O exemplo usa `WEBMANIA_BASE_URL` como configuração opcional; deixe-a ausente para o serviço padrão e use outra origem somente quando controlada e autorizada. `Config.BaseURL` aprova um único endereço HTTPS e prefixo de serviço (por exemplo `https://webmania.com.br/api` para a API1, `https://nfe.webmania.com.br` para arquivos API1 e `https://api.webmania.com.br` para API2, arquivos API2 e relatórios atuais); usuário, query, fragmento e segmentos `.`/`..` são recusados antes da rede. `RootCAs` permite uma autoridade privada, mantendo verificação TLS e hostname. HTTP fica restrito ao loopback de testes. Não há proxy automático, cookies, redirecionamento, repetição de chamada nem confiança adquirida de uma URL retornada. O transporte abre uma conexão por chamada e escreve diretamente nela; não usa o mecanismo de reenvio de `http.Transport`.

`FileResponse` preserva bytes e tipo de mídia. Relatórios incluem CSV e XLSX declarados; no download legado (`DownloadReport`), CSV e XLSX rotulados `text/xml` são identificados pelos bytes (assinatura ZIP = XLSX, qualquer outro corpo = CSV), com o formato detectado em `MediaType`; XML, HTML ou JSON nesse download é recusado e nenhuma outra operação ganha essa tolerância. CSV recebe análise de registros sem decodificação (UTF-8, com ou sem BOM, ou Latin-1) e XLSX recebe reconhecimento limitado da estrutura SpreadsheetML, com até 10.000 partes, 64 MiB descompactados e profundidade XML 128. Não há avaliação de fórmulas nem busca de relações externas. O status e a mídia precisam corresponder à resposta exata, classe `2XX` ou `default` declarados, nessa ordem. `Protected` distingue proteção reconhecida em PDF/ZIP; não há descriptografia ou extração automática. HTML/JSON não se tornam PDF/XML por causa do MIME. PDF recebe inspeção estrutural limitada; a SDK não é um validador PDF completo. Um `302` documentado oferece `Location.Reveal()` deliberadamente, sem buscar o destino ou declarar arquivo pronto. Use uma operação fixa em um cliente aprovado separadamente para obter outro recurso.

Erros têm `Kind`, `Operation`, `Status`, `Outcome`, diagnósticos limitados e correlação sanitizada. `errors.Is(err, context.Canceled)` e `errors.Is(err, context.DeadlineExceeded)` preservam interrupção. Uma mutação enviada com resultado incerto tem `Outcome == "unknown"`; consulte o resultado anterior antes de qualquer decisão de novo envio. Isso também vale para GETs com efeito declarado. `Reference.Reveal()` recupera explicitamente uma cópia dos seletores seguros fornecidos pelo chamador. Credenciais, capacidades conhecidas e colisões não viram seletores de recuperação. Headers incompletos removem a referência; uma falha de tamanho do corpo após headers completos conserva a recuperação comum segura.

`MonitorClient.Companies`, `CompanyMunicipalities` e `Reports` retornam iteradores tipados com limite de itens, páginas e prazo. `per_page` permanece constante; a página inicial explícita é respeitada em `int64` até `MaxInt64-1`. `Continuation()` fornece página e índice; `StopReason()` distingue `complete`, `item_limit` e `page_limit`. Cancelamento e prazo são verificados também ao entregar itens já recebidos.

`NfeClient.WaitReport` congela o mesmo pedido e usa apenas a operação documentada de criação/consulta de relatório. `MonitorClient.WaitImport` consulta uma importação já submetida. Ambos exigem intervalo mínimo de 5 segundos e limites de tentativas/prazo. O contexto de capacidades de relatório pertence a uma única espera. Emissão, cancelamento e submissão de importação não recebem repetição automática.

## Credenciais e resultado fiscal

Credenciais da empresa API1, revenda API1, Bearer do emissor API2 e Bearer Monitor pertencem a escopos diferentes. Arquivos exigem a modalidade de acesso documentada. Uma falha não autoriza trocar automaticamente o modo ou a credencial. Mantenha segredos no servidor.

O catálogo contém 102 operações habilitadas. `nfeDownloadIbsCbsEventXml` não está disponível nesta versão do SDK: a chamada falha com `capability` e nenhuma requisição é enviada. `ReportFilesClient.DownloadReportV2` baixa relatórios atuais (`GET /relatorios/{uuid}` na API2) com `AnonymousFileAuth()` (a URL é a capability), `CompanyFileAuth()` (`NewReportFilesClient` com credenciais da empresa) ou `IssuerFileAuth()` (`NewIssuerReportFilesClient` com o Bearer API2 da empresa que exportou o relatório); um JSON com `error`, mesmo em HTTP 200, é falha. Condições fiscais, municipais, de cadastro e de provedor continuam sujeitas ao contrato e ao serviço. Validação local, cálculo ou prévia não comprovam autorização fiscal.

Não há retry automático de operação nem seguimento automático de redirects. Depois de possível envio de uma operação com efeito, uma falha pode indicar resultado desconhecido. Preserve o seletor original com segurança, consulte a tentativa anterior e decida explicitamente como prosseguir. Uma referência protegida exige acesso deliberado e pode estar ausente; não é uma chave de idempotência.

## Arquivos e processamento

Verifique tipo de resultado, status e representação antes de consumir bytes. JSON pendente, HTML e redirect não comprovam PDF pronto. Proteção de PDF/ZIP é distinta da autorização de download; não há descriptografia automática nem confirmação de autenticidade fiscal. O reconhecimento de formatos cobre somente o perfil documentado desta implementação. Uma Location revelada continua privada e exige decisão explícita de origem e autenticação para outra transferência.

Paginação e espera são optativas e limitadas quando disponíveis. Um estado futuro ou desconhecido não significa conclusão. Não repita emissão, cancelamento ou importação inicial como se fossem consultas de processamento.

## Documentação para assistentes e contratos

Índices em Markdown para assistentes: [documentação Webmania](https://webmania.com.br/docs/llms.txt) e [API 2.0](https://api.webmania.com.br/llms.txt). Contratos OpenAPI: [API 2.0](https://api.webmania.com.br/docs/openapi) e [NF-e](https://webmania.com.br/docs/rest-api-nfe/?format=openapi).

No catálogo de SDKs, `go` aparece como disponível no repositório público, com instalação pelo clone; plataforma Go 1.26. Estar listado nesses índices não amplia a cobertura documentada deste SDK.
