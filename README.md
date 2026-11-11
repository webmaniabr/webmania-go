<p align="center"><img src=".github/assets/webmania-logo.png" alt="Webmania" width="320"></p>

# Webmania SDK para Go

Através do emissor fiscal da Webmania®, você conta com a emissão e o arquivamento dos seus documentos fiscais, cálculo automático de impostos, geração do DANFE para impressão e envio automático de e-mails para os clientes. Com este SDK, o seu sistema em Go se integra à nossa REST API para emitir, consultar e cancelar NF-e, NFC-e, NFS-e, CT-e, MDF-e, NFCom e DC-e, baixar XML e PDF dos documentos e usar o Monitor Fiscal.

- Emissor de Nota Fiscal Webmania®: [Saiba mais](https://webmania.com.br/nota-fiscal-eletronica/)
- Documentação da REST API: [Visualizar](https://webmania.com.br/docs/)
- Índice da documentação para assistentes de IA: [llms.txt](https://webmania.com.br/docs/llms.txt)

## Requisitos

- Contrate um dos planos de Nota Fiscal Eletrônica da Webmania®: [Assine agora mesmo](https://webmania.com.br/nota-fiscal-eletronica/#plans-section).
- Go 1.26 ou superior. Para gerar o pacote a partir do clone, Python 3 e git.
- Credenciais de acesso da sua empresa, disponíveis no [Painel de Controle](https://webmania.com.br/painel/).

## Instalação

O SDK é distribuído pelo repositório [github.com/webmaniabr/webmania-go](https://github.com/webmaniabr/webmania-go) e ainda não tem versão publicada no proxy público de módulos Go. Clone o repositório e gere uma pasta de proxy local do módulo:

```sh
git clone https://github.com/webmaniabr/webmania-go.git
python3 webmania-go/scripts/qualify.py --build-only --output "$PWD/webmania-go-proxy"
```

No projeto da sua aplicação:

```sh
GOPROXY="file:///caminho/webmania-go-proxy/proxy,https://proxy.golang.org" \
GONOSUMDB=github.com/webmaniabr/webmania-go \
go get github.com/webmaniabr/webmania-go@v0.1.0-alpha.5
```

A exceção de checksum (`GONOSUMDB`) vale só para este módulo; as demais dependências continuam verificadas normalmente.

## Credenciais

Cada serviço usa a sua própria credencial. Guarde-as em variáveis de ambiente (ou em um cofre de segredos) no servidor e nunca as escreva no código-fonte:

| Variável de ambiente | Uso |
|---|---|
| `WEBMANIA_CONSUMER_KEY`, `WEBMANIA_CONSUMER_SECRET`, `WEBMANIA_ACCESS_TOKEN`, `WEBMANIA_ACCESS_TOKEN_SECRET` | NF-e e NFC-e (`NewNfeClient`) e arquivos da NF-e (`NewNfeFilesClient`) |
| `WEBMANIA_ISSUER_TOKEN` | NFS-e, CT-e, MDF-e, NFCom e DC-e (`NewEmitterClient`) e seus arquivos (`NewEmitterFilesClient`) |
| `WEBMANIA_MONITOR_TOKEN` | Monitor Fiscal (`NewMonitorClient`) |

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	wm "github.com/webmaniabr/webmania-go"
	fiscal "github.com/webmaniabr/webmania-go/model/api1fiscal"
	nfse "github.com/webmaniabr/webmania-go/model/api2nfseissuance"
	"github.com/webmaniabr/webmania-go/value"
)

ctx := context.Background()
credenciais := wm.CompanyCredentials{
	ConsumerKey:       os.Getenv("WEBMANIA_CONSUMER_KEY"),
	ConsumerSecret:    os.Getenv("WEBMANIA_CONSUMER_SECRET"),
	AccessToken:       os.Getenv("WEBMANIA_ACCESS_TOKEN"),
	AccessTokenSecret: os.Getenv("WEBMANIA_ACCESS_TOKEN_SECRET"),
}
nfe, err := wm.NewNfeClient(credenciais, wm.Config{}) // NF-e e NFC-e
if err != nil {
	panic(err)
}
emissor, err := wm.NewEmitterClient(wm.IssuerCredentials{Token: os.Getenv("WEBMANIA_ISSUER_TOKEN")}, wm.Config{}) // NFS-e, CT-e, MDF-e, NFCom e DC-e
if err != nil {
	panic(err)
}
```

Os trechos seguintes ficam dentro de uma função da sua aplicação. Cada método recebe um `context.Context` e devolve `*wm.Result[T]` (`Value` com a resposta tipada e `Status` com o status HTTP); os campos opcionais são lidos com `Get()`.

## Utilização

Os exemplos abaixo usam o ambiente de **homologação** (`ambiente` 2), em que os documentos não têm validade fiscal. Para emitir em produção, envie `ambiente` 1. Os pedidos são montados a partir de JSON; informe valores decimais como texto (`"44.90"`) para manter a precisão.

### Status do SEFAZ e certificado A1

```go
sefaz, err := nfe.GetSefazStatus(ctx, wm.NfeGetSefazStatusParams{})
if err == nil {
	status, _ := sefaz.Value.Status.Get()
	fmt.Println(status) // online ou offline
}

certificado, err := nfe.GetCertificateValidity(ctx, wm.NfeGetCertificateValidityParams{})
if err == nil {
	dias, _ := certificado.Value.Expiration.Get()
	fmt.Println(dias) // dias até o vencimento do certificado A1
}
```

### Emitir NF-e

Para emitir uma NF-e, monte o corpo a partir do JSON da nota e use `IssueDocument`. O mesmo método emite NFC-e com `"modelo": 2`.

```go
var dados fiscal.NfeIssueDocumentBody
if err := json.Unmarshal([]byte(`{
  "operacao": 1,
  "natureza_operacao": "Venda de produção do estabelecimento",
  "modelo": 1,
  "finalidade": 1,
  "ambiente": 2,
  "cliente": {
    "cpf": "000.000.000-00", "nome_completo": "Nome do Cliente",
    "endereco": "Av. Brg. Faria Lima", "numero": "1000", "bairro": "Itaim Bibi",
    "cidade": "São Paulo", "uf": "SP", "cep": "00000-000", "email": "nome@email.com"
  },
  "produtos": [{
    "nome": "Nome do produto", "codigo": "nome-do-produto", "ncm": "6109.10.00",
    "quantidade": "1", "unidade": "UN", "origem": 0,
    "subtotal": "44.90", "total": "44.90", "classe_imposto": "REF1000"
  }],
  "pedido": {"presenca": 2, "modalidade_frete": 9, "pagamento": 0, "forma_pagamento": "01", "total": "44.90"}
}`), &dados); err != nil {
	panic(err)
}

emissao, err := nfe.IssueDocument(ctx, wm.NfeIssueDocumentParams{Body: dados})
if err != nil {
	panic(err)
}
nota := emissao.Value
uuid, _ := nota.Uuid.Get()     // identificador único da nota fiscal
status, _ := nota.Status.Get() // aprovado, reprovado, processamento...
chave, _ := nota.Chave.Get()   // chave de acesso
xml, _ := nota.Xml.Get()       // URL do XML
danfe, _ := nota.Danfe.Get()   // URL do DANFE (PDF)
motivo, _ := nota.Motivo.Get() // retorno do SEFAZ
fmt.Println(uuid, status, chave, xml, danfe, motivo)
```

Somente o status `aprovado` indica uma nota autorizada. Quando o retorno for `processamento`, consulte a nota alguns segundos depois.

### Consultar NF-e

```go
consulta, err := nfe.GetDocument(ctx, wm.NfeGetDocumentParams{Query: fiscal.NfeGetDocumentQuery{Uuid: value.Some(uuid)}})
if err == nil {
	if documento, ok := consulta.Value.First(); ok {
		situacao, _ := documento.Status.Get()
		fmt.Println(situacao)
	}
}
```

A consulta também aceita a chave de acesso (campo `Chave`).

### Cancelar NF-e

```go
cancelamento, err := nfe.CancelDocument(ctx, wm.NfeCancelDocumentParams{Body: fiscal.NfeCancelDocumentBody{
	Uuid:   value.Some(uuid),
	Motivo: value.Some("Cancelamento por erro no pedido."), // mínimo de 15 caracteres
}})
if err == nil {
	situacao, _ := cancelamento.Value.Status.Get()
	fmt.Println(situacao)
}
```

### Baixar o DANFE e o XML

```go
arquivos, err := wm.NewNfeFilesClient(&credenciais, wm.Config{})
if err != nil {
	panic(err)
}
if pdf, err := arquivos.DownloadDanfe(ctx, wm.NfeDownloadDanfeParams{Identifier: uuid, Auth: wm.CompanyFileAuth()}); err == nil {
	_ = os.WriteFile("danfe.pdf", pdf.Data, 0o600)
}
if xmlNfe, err := arquivos.DownloadXml(ctx, wm.NfeDownloadXmlParams{Identifier: uuid, Auth: wm.CompanyFileAuth()}); err == nil {
	_ = os.WriteFile("nfe.xml", xmlNfe.Data, 0o600)
}
```

### Emitir, consultar e cancelar NFS-e

A NFS-e é emitida a partir de um ou mais RPS com o método `IssueNfse` do `EmitterClient`:

```go
var rps nfse.EmitterIssueNfseBody
if err := json.Unmarshal([]byte(`{
  "ambiente": 2,
  "rps": [{
    "servico": {
      "valor_servicos": "15.00",
      "discriminacao": "Prestação de serviço referente ao mês 10/2026",
      "classe_imposto": "REF000000"
    },
    "tomador": {"cpf": "000.000.000-00", "nome_completo": "Nome do tomador"}
  }]
}`), &rps); err != nil {
	panic(err)
}
emitida, err := emissor.IssueNfse(ctx, wm.EmitterIssueNfseParams{Body: rps})
if err != nil {
	panic(err)
}
uuidNfse, _ := emitida.Value.Uuid.Get()

consultaNfse, err := emissor.GetNfse(ctx, wm.EmitterGetNfseParams{Identifier: uuidNfse})
if err == nil {
	situacao, _ := consultaNfse.Value.Status.Get()
	fmt.Println(situacao)
}

var cancela nfse.EmitterCancelNfseBody
_ = json.Unmarshal([]byte(`{"uuid": "`+uuidNfse+`", "motivo": "1", "justificativa": "Serviço cancelado a pedido do tomador."}`), &cancela)
// motivo: código do motivo (veja a documentação da NFS-e); justificativa: mínimo de 15 caracteres
if cancelada, err := emissor.CancelNfse(ctx, wm.EmitterCancelNfseParams{Body: cancela}); err == nil {
	situacao, _ := cancelada.Value.Status.Get()
	fmt.Println(situacao)
}
```

### Tratamento de erros

As falhas são do tipo `*wm.Error`:

```go
if _, err := nfe.IssueDocument(ctx, wm.NfeIssueDocumentParams{Body: dados}); err != nil {
	var erro *wm.Error
	if errors.As(err, &erro) {
		fmt.Println(erro.Kind)   // validation (dados recusados antes do envio), business (recusa da API ou do SEFAZ), http, timeout...
		fmt.Println(erro.Status) // status HTTP, quando houver
		if erro.Outcome == "unknown" {
			// A requisição pode ter sido processada: consulte a nota antes de emitir de novo.
		}
	}
}
```

O SDK não repete requisições automaticamente. Se uma emissão ou um cancelamento terminar com resultado desconhecido (`Outcome == "unknown"`), consulte o documento antes de tentar novamente, para não emitir em duplicidade.

## Métodos disponíveis

**NF-e e NFC-e** — `NfeClient`

- **IssueDocument**: Emissão de NF-e ou NFC-e junto ao SEFAZ.
- **GetDocument**: Consulta da nota fiscal pelo UUID, pela chave de acesso ou pelo ID do pedido.
- **CancelDocument**: Cancelamento de NF-e ou NFC-e.
- **CreateCorrection**: Carta de correção.
- **VoidNumbering**: Inutilização de sequência de numeração.
- **IssueReturn**: Emissão de NF-e de devolução.
- **IssueComplement**: Emissão de NF-e complementar.
- **IssueAdjustment**: Emissão de NF-e de ajuste.
- **CreateManifestation**: Manifestação do destinatário.
- **CreatePaymentEvent** / **CancelPaymentEvent**: Evento de conciliação financeira (ECONF) e o seu cancelamento.
- **CreateIbsCbsEvent** / **CancelIbsCbsEvent**: Eventos de IBS/CBS e o seu cancelamento.
- **ListTaxClasses** / **SaveTaxClasses** / **DeleteTaxClasses**: Classes de imposto.
- **CreateOrPollReport**: Relatórios fiscais.
- **UpdateCompany**: Atualização dos dados da empresa emissora.
- **GetSefazStatus**: Verifica se o SEFAZ está online ou offline.
- **GetCertificateValidity**: Verifica quantos dias faltam para o certificado A1 expirar.

**NFS-e** — `EmitterClient`

- **IssueNfse**: Emissão de NFS-e a partir de RPS.
- **GetNfse**: Consulta da NFS-e.
- **CancelNfse**: Cancelamento da NFS-e.
- **ReplaceNfse**: Substituição de NFS-e.
- **ManifestNfse**: Manifestação sobre NFS-e nacional.
- **GetNfseProviderStatus**: Recursos do provedor de NFS-e do município da empresa.

**CT-e e CT-e OS** — `EmitterClient`

- **IssueCte** / **IssueSimplifiedCte**: Emissão de CT-e, CT-e OS e CT-e simplificado.
- **GetCte**: Consulta do CT-e.
- **CancelCte**: Cancelamento do CT-e.
- **CorrectCte**: Carta de correção do CT-e.
- **ConfirmCteDelivery** / **CancelCteDelivery**: Comprovante de entrega e o seu cancelamento.
- **LinkCtePayment** / **CancelCtePaymentLink** / **GetCtePaymentEvents**: Eventos de pagamento do CT-e.

**MDF-e** — `EmitterClient`

- **IssueMdfe**: Emissão do MDF-e.
- **GetMdfe**: Consulta do MDF-e pela chave de acesso.
- **CloseMdfe**: Encerramento do MDF-e.
- **CancelMdfe**: Cancelamento do MDF-e.
- **AddMdfeConductor**: Inclusão de condutor.

**NFCom e DC-e** — `EmitterClient`

- **IssueNfcom** / **GetNfcom** / **CancelNfcom**: Emissão, consulta e cancelamento da NFCom.
- **GetNfcomPaymentEvents**: Eventos de pagamento da NFCom.
- **GetNfcomServiceStatus**: Disponibilidade do serviço da NFCom no SEFAZ.
- **IssueDce** / **GetDce** / **CancelDce**: Emissão, consulta e cancelamento da DC-e.

**Arquivos (XML e PDF)** — `NfeFilesClient` e `EmitterFilesClient`

- **DownloadDanfe** / **DownloadSimpleDanfe** / **DownloadLabelDanfe**: DANFE completo, simplificado e etiqueta.
- **DownloadXml** / **DownloadCancellationXml**: XML da NF-e e do cancelamento.
- **DownloadCorrectionPdf** / **DownloadCorrectionXml**: PDF e XML da carta de correção.
- **GetNfseXml** / **GetNfsePdf** / **GetNfseRpsPdf**: XML e PDF da NFS-e e PDF do RPS.
- **GetCteXml** / **GetCtePdf** / **GetMdfeXml** / **GetMdfePdf** / **GetNfcomXml** / **GetNfcomPdf** / **GetDceXml** / **GetDcePdf**: XML e PDF de CT-e, MDF-e, NFCom e DC-e.

**Monitor Fiscal** — `MonitorClient`

- **SearchDocuments** / **GetDocument** / **ListDocumentEvents**: Busca e consulta de documentos recebidos e do histórico de eventos.
- **ManifestDocument**: Manifestação do destinatário.
- **ImportDocument** / **GetImportStatus**: Importação por chave de acesso, XML ou ZIP.
- **ListCompanies** / **CreateCompany** / **UpdateCompany**: Empresas monitoradas.

**Gerencial B2B** — `ResellerClient`

- **CreateCompanies** / **ListCompanies** / **GetProcessedRequests**: Cadastro de empresas, listagem e contagem de notas processadas por empresa.

A lista completa, com todos os métodos do Monitor Fiscal e dos arquivos, está em [docs/OPERATIONS.md](docs/OPERATIONS.md).

## Exemplo completo em homologação

A pasta [examples/homologacao](examples/homologacao) traz um programa que emite, consulta, baixa os arquivos e cancela uma NF-e e uma NFS-e em homologação, usando as credenciais das variáveis de ambiente. Por segurança, ele só envia requisições quando `WEBMANIA_LIVE=1` está definido e `WEBMANIA_LIVE_ALLOWED_HOSTS` lista os hosts da API. Para rodar só as consultas (status do SEFAZ, certificado e provedor de NFS-e):

```sh
WEBMANIA_LIVE=1 WEBMANIA_LIVE_SCOPE=readonly WEBMANIA_LIVE_ALLOWED_HOSTS=webmania.com.br,nfe.webmania.com.br,api.webmania.com.br sh examples/homologacao/run.sh
```

As variáveis aceitas e o formato da saída estão em [examples/homologacao/README.md](examples/homologacao/README.md).

## Documentação

- Documentação da REST API: [NF-e / NFC-e](https://webmania.com.br/docs/rest-api-nfe/) · [NFS-e](https://webmania.com.br/docs/rest-api-nfse/) · [CT-e](https://webmania.com.br/docs/rest-api-cte/) · [MDF-e](https://webmania.com.br/docs/rest-api-mdfe/) · [NFCom](https://webmania.com.br/docs/rest-api-nfcom/) · [DC-e](https://webmania.com.br/docs/rest-api-dce/) · [Monitor Fiscal](https://webmania.com.br/docs/rest-api-monitor-fiscal/) · [Gerencial B2B](https://webmania.com.br/docs/rest-api-gerencial-b2b/)
- Contratos OpenAPI: [API 2.0](https://api.webmania.com.br/docs/openapi) e [NF-e](https://webmania.com.br/docs/rest-api-nfe/?format=openapi)
- Detalhes técnicos do SDK (URL base, arquivos, relatórios, limites): [docs/INTEGRATION.md](docs/INTEGRATION.md)
- Lista completa de métodos: [docs/OPERATIONS.md](docs/OPERATIONS.md)
- Migração de integrações anteriores: [docs/MIGRATION.md](docs/MIGRATION.md)
- Desenvolvimento e testes do SDK: [docs/TESTING.md](docs/TESTING.md)
- Histórico de versões: [CHANGELOG.md](CHANGELOG.md)

## Suporte

Qualquer dúvida entre em contato na nossa [Central de Ajuda](https://ajuda.webmania.com.br) ou acesse o [Painel de Controle](https://webmania.com.br/painel/) para conversar em tempo real no Chat ou Abrir um chamado.

## Licença

Licença proprietária Webmania®: veja [LICENSE](LICENSE) e os [Termos de Uso](https://webmania.com.br/termos-de-uso/). As dependências de terceiros mantêm as suas próprias licenças.
