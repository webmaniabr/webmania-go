# Migração para o SDK Go

O novo módulo é independente dos SDKs e exemplos anteriores. Nenhum repositório legado foi renomeado ou alterado por esta prévia. O caminho do módulo é `github.com/webmaniabr/webmania-go`.

Substitua chamadas HTTP montadas pela aplicação por um cliente do serviço, tipos em `model/<modulo>` e o wrapper `...Params` da operação. Headers API 1 de empresa e revenda, Bearer de emissão, Bearer Monitor e tokens de documento têm tipos/modos distintos. Configure somente o serviço usado. Não copie um Bearer para `token` na query.

Preserve nomes JSON, identificadores textuais e precisão: `Field[T]` distingue omissão/null/zero/false e `json.Number`/`value.Scalar` evita coerção por ponto flutuante. Campos novos ficam em `Extras`; colisões e propriedades proibidas pelo contrato falham antes da conexão. Os campos comuns, como `NfeCancelRequest.Motivo`, continuam tipados mesmo quando o contrato tem alternativas de identificação.

Resultados retornam modelo tipado em `Result.Value` e status HTTP em `Result.Status`. Leia o status fiscal explícito do modelo; HTTP 200 ou processamento não significa autorização. Troque captura de texto livre por `errors.As` para `*webmania.Error`, `Kind`, `Outcome` e `errors.Is` para cancelamento. Para uma mutação incerta, use deliberadamente `Reference.Reveal()` na consulta aplicável; não reenvie a emissão por causa de timeout.

Downloads usam métodos fixos e `FileAuth` explícito. Não há helper que siga URLs arbitrárias de resposta. Relatórios atuais usam `NewReportFilesClient` (credenciais da empresa) ou `NewIssuerReportFilesClient` (Bearer API2 da empresa) e `DownloadReportV2` com o UUID da `url` devolvida, na origem configurada. `Location.Reveal()` retorna somente metadado privado; exige revisão do destino e configuração independente para uma busca posterior. Arquivos privados precisam de tratamento e armazenamento pela aplicação.

Use os iteradores limitados do Monitor e as esperas de relatório/importação em vez de loops sem limite. O mesmo pedido de relatório é congelado; apenas seu polling documentado é repetido. Não há retry de emissão, evento ou importação.

Prepare os dados válidos da própria conta para os exemplos de homologação. O repositório contém corpus sintético exclusivo para loopback, sem autorização para envio à Webmania. A validação fiscal real e a comparação funcional por operação do legado permanecem pendentes.

## alpha.4 para alpha.5

Os modelos de corpo das emissões CT-e, DC-e, MDF-e, NFCom e NFS-e seguem os nomes derivados da operação (`EmitterIssueNfseBody` continua sendo o alias do corpo, agora de `EmitterIssueNfsePayload`); campos e validação não mudam. `DownloadReport` aceita CSV e XLSX rotulados `text/xml` (formato detectado pelos bytes, em `MediaType`) e CSV em Latin-1 sem alterar bytes. Downloads de relatórios atuais usam `ReportFilesClient`.
